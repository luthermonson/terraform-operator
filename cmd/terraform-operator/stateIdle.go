package main

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	tftype "github.com/danisla/terraform-operator/pkg/types"
)

func stateIdle(parentType ParentType, parent *tftype.Terraform, status *tftype.TerraformOperatorStatus, children *TerraformOperatorRequestChildren, desiredChildren *[]interface{}) (tftype.TerraformOperatorState, error) {
	var err error

	// Wait for any specFrom resource.
	var specFromType, specFromName string
	if parent.SpecFrom.TFPlan != "" {
		specFromType = "TerraformPlan"
		specFromName = parent.SpecFrom.TFPlan
	} else if parent.SpecFrom.TFApply != "" {
		specFromType = "TerraformApply"
		specFromName = parent.SpecFrom.TFApply
	} else if parent.SpecFrom.TFDestroy != "" {
		specFromType = "TerraformDestroy"
		specFromName = parent.SpecFrom.TFDestroy
	}
	if specFromType != "" {
		specFromTF, err := getTerraform(specFromType, parent.GetNamespace(), specFromName)
		if err != nil {
			myLog(parent, "INFO", fmt.Sprintf("Waiting for %s %s spec to become available.", specFromType, specFromName))
			return StateSpecFromPending, nil
		}
		if status.StateCurrent == StateSpecFromPending {
			myLog(parent, "INFO", fmt.Sprintf("Using spec from %s %s", specFromType, specFromName))
		}
		parent.Spec = specFromTF.Spec
	}

	if status.StateCurrent == StateIdle && !changeDetected(parent, children, status) {
		return StateIdle, nil
	}

	if active, _, _, _ := getPodStatus(children.Pods); active > 0 {
		// Pods should only be active in the StatePodRunning or StateRetry states.
		return StateNone, fmt.Errorf("pods active in StateIdle, re-sync collision")
	}

	// Generate new ordinal pod name
	podName := makeOrdinalPodName(parentType, parent, children)

	// Map of provider config secret names to list of key names.
	providerConfigKeys := make(map[string][]string, 0)

	// Check for provider config secret. If not yet available, transition to StateProviderConfigPending
	if parent.Spec.ProviderConfig != nil {
		for _, c := range parent.Spec.ProviderConfig {
			if c.SecretName != "" {
				secretKeys, err := getProviderConfigSecret(parent.ObjectMeta.Namespace, c.SecretName)
				if err != nil {
					// Wait for secret to become available
					return StateProviderConfigPending, nil
				}
				providerConfigKeys[c.SecretName] = secretKeys
			}
		}
	}

	// Wait for all config sources
	sourceData, err := getSourceData(parent, desiredChildren, podName)
	if err != nil {
		myLog(parent, "WARN", fmt.Sprintf("%v", err))
		return StateSourcePending, nil
	}

	// Wait for any TFInputs
	tfInputVars, err := getTFInputs(parent)
	if err != nil {
		myLog(parent, "WARN", fmt.Sprintf("%v", err))
		return StateTFInputPending, nil
	}

	// Wait for any TFVarsFrom sources
	tfVarsFrom, err := getTFVarsFrom(parent)
	if err != nil {
		myLog(parent, "WARN", fmt.Sprintf("%v", err))
		return StateTFVarsFromPending, nil
	}

	// Wait for any TerraformPlan
	tfplanFile, err := getTFPlanFile(parent)
	if err != nil {
		myLog(parent, "WARN", fmt.Sprintf("%v", err))
		return StateTFPlanPending, nil
	}

	// Get the image and pull policy (or default) from the spec.
	image, imagePullPolicy := getImageAndPullPolicy(parent)

	// Get the backend bucket and backend prefix (or default) from the spec.
	backendBucket, backendPrefix := getBackendBucketandPrefix(parent)

	// Terraform Pod data
	tfp := TFPod{
		Image:              image,
		ImagePullPolicy:    imagePullPolicy,
		Namespace:          parent.Namespace,
		ProjectID:          config.Project,
		Workspace:          fmt.Sprintf("%s-%s", parent.Namespace, parent.Name),
		SourceData:         sourceData,
		ProviderConfigKeys: providerConfigKeys,
		BackendBucket:      backendBucket,
		BackendPrefix:      backendPrefix,
		TFParent:           parent.Name,
		TFPlan:             tfplanFile,
		TFInputs:           tfInputVars,
		TFVars:             parent.Spec.TFVars,
		TFVarsFrom:         tfVarsFrom,
	}

	status.Sources.ConfigMapHashes = *sourceData.ConfigMapHashes
	status.Sources.EmbeddedConfigMaps = *sourceData.EmbeddedConfigMaps

	// Make Terraform Pod
	var pod corev1.Pod
	switch parentType {
	case ParentPlan:
		pod, err = tfp.makeTerraformPod(podName, []string{PLAN_POD_CMD})
	case ParentApply:
		pod, err = tfp.makeTerraformPod(podName, []string{APPLY_POD_CMD})
	case ParentDestroy:
		pod, err = tfp.makeTerraformPod(podName, []string{DESTROY_POD_CMD})
	default:
		// This should not happen.
		myLog(parent, "WARN", fmt.Sprintf("Unhandled parentType in StateIdle: %s", parentType))
	}
	if err != nil {
		myLog(parent, "ERROR", fmt.Sprintf("Failed to generate terraform pod: %v", err))
		return StateIdle, nil
	}

	*desiredChildren = append(*desiredChildren, pod)

	status.PodName = pod.Name
	status.Workspace = tfp.Workspace
	status.StateFile = makeStateFilePath(tfp.BackendBucket, tfp.BackendPrefix, tfp.Workspace)
	status.TFPlan = ""
	status.TFOutput = make(map[string]tftype.TerraformOutputVar, 0)
	status.StartedAt = ""
	status.FinishedAt = ""
	status.Duration = ""
	status.PodStatus = tftype.PodStatusUnknown

	myLog(parent, "INFO", fmt.Sprintf("Created Pod: %s", pod.Name))

	// Transition to StatePodRunning
	return StatePodRunning, nil
}

func getPodStatus(pods map[string]corev1.Pod) (int, int, int, string) {
	lastActiveName := ""
	active := 0
	succeeded := 0
	failed := 0
	for _, pod := range pods {
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			succeeded++
		case corev1.PodFailed:
			failed++
		default:
			lastActiveName = pod.Name
			active++
		}
	}
	return active, succeeded, failed, lastActiveName
}
