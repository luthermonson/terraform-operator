package main

import (
	"fmt"
	"sort"
	"strings"

	tfv1 "github.com/danisla/terraform-operator/pkg/types"
	corev1 "k8s.io/api/core/v1"
)

const (
	// StateNone is the inital state for a new spec.
	StateNone = tfv1.TerraformOperatorState("NONE")
	// StateIdle means there are no more changes pending
	StateIdle = tfv1.TerraformOperatorState("IDLE")
	// StateWaitComplete is used to indicate that a wait is complete and to transition back through the idle handler.
	StateWaitComplete = tfv1.TerraformOperatorState("WAIT_COMPLETE")
	// StateSpecFromPending means the controller is waiting for the input spec resource to become available.
	StateSpecFromPending = tfv1.TerraformOperatorState("SPEC_FROM_PENDING")
	// StateSourcePending means the controller is waiting for the source ConfigMap to become available.
	StateSourcePending = tfv1.TerraformOperatorState("SOURCE_PENDING")
	// StateProviderConfigPending means the controller is waiting for the credentials Secret to become available.
	StateProviderConfigPending = tfv1.TerraformOperatorState("PROVIDER_PENDING")
	// StateTFPlanPending means the controller is waiting for tfplan object.
	StateTFPlanPending = tfv1.TerraformOperatorState("TFPLAN_PENDING")
	// StateTFInputPending means the controller is waiting for one or more tfapply objects.
	StateTFInputPending = tfv1.TerraformOperatorState("TFINPUT_PENDING")
	// StateTFVarsFromPending means the controller is waiting to read tfvars from another object.
	StateTFVarsFromPending = tfv1.TerraformOperatorState("TFVARSFROM_PENDING")
	// StatePodRunning means the controller is waiting for the terraform pod to complete.
	StatePodRunning = tfv1.TerraformOperatorState("POD_RUNNING")
	// StateRetry means a pod has failed and is being retried up to MaxAttempts times.
	StateRetry = tfv1.TerraformOperatorState("POD_RETRY")
)

// ParentType represents the strign mapping to the possible parent types in the const below.
type ParentType string

const (
	ParentPlan    = "tfplan"
	ParentApply   = "tfapply"
	ParentDestroy = "tfdestroy"
)

// SyncRequest describes the payload from the CompositeController hook
type SyncRequest struct {
	Parent   tfv1.Terraform    `json:"parent"`
	Children TerraformChildren `json:"children"`
}

// SyncResponse is the CompositeController response structure.
type SyncResponse struct {
	Status   tfv1.TerraformOperatorStatus `json:"status"`
	Children []interface{}                `json:"children"`
}

// TerraformChildren is the children definition passed by the CompositeController request for the Terraform controller.
type TerraformChildren struct {
	Pods       map[string]corev1.Pod       `json:"Pod.v1"`
	ConfigMaps map[string]corev1.ConfigMap `json:"ConfigMap.v1"`
	Secrets    map[string]corev1.Secret    `json:"Secret.v1"`
}

func (children *TerraformChildren) claimChildAndGetCurrent(newChild interface{}, desiredChildren *[]interface{}) interface{} {
	var currChild interface{}
	switch o := newChild.(type) {
	case corev1.Pod:
		if child, ok := children.Pods[o.GetName()]; ok == true {
			currChild = child
		}
	case corev1.ConfigMap:
		if child, ok := children.ConfigMaps[o.GetName()]; ok == true {
			currChild = child
		}
	case corev1.Secret:
		if child, ok := children.Secrets[o.GetName()]; ok == true {
			currChild = child
		}
	}

	*desiredChildren = append(*desiredChildren, newChild)

	return currChild
}

// ProviderConfigKeys is a map of secret names to a list of keys in the secret.
type ProviderConfigKeys map[string][]string

// TerraformInputVars is a map of output var names from TerraformApply Objects.
type TerraformInputVars map[string]string

// TerraformSpecCredentials is the structure for providing the credentials
type TerraformSpecCredentials struct {
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

// TerraformConfigSourceData is the structure of all of the extracted config sources used by the Terraform Pod.
type TerraformConfigSourceData struct {
	ConfigMapHashes    map[string]tfv1.ConfigMapHash
	ConfigMapKeys      tfv1.ConfigMapKeys
	GCSObjects         tfv1.GCSObjects
	EmbeddedConfigMaps tfv1.EmbeddedConfigMaps
}

// ConfigMapSourceData is an internal structure for mapping config map keys to strings and performing validation and hashing.
type ConfigMapSourceData map[string]string

// Validate verifies that there is at least 1 key in the configmap.
func (c *ConfigMapSourceData) Validate() error {
	if c != nil && len(*c) == 0 {
		return fmt.Errorf("no data found in ConfigMap")
	}
	return nil
}

// GetHash returns a hash of the config map source data.
func (c *ConfigMapSourceData) GetHash() string {
	// Create a stable hash from the map[string]string using a stringified (name, hash) sorted tuple.
	tuples := make([]string, 0)
	for k, v := range *c {
		tuples = append(tuples, strings.Join([]string{k, toSha1(v)}, ","))
	}
	sort.Strings(tuples)
	// return the hash of the sorted tuples.
	return toSha1(strings.Join(tuples, ","))
}
