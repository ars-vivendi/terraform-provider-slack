// Package v1beta1 contains Slack provider configuration APIs.
// +kubebuilder:object:generate=true
// +groupName=slack.m.arsvivendi.io
package v1beta1

import (
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderConfigSpec selects the JSON credential Secret.
type ProviderConfigSpec struct {
	Credentials ProviderCredentials `json:"credentials"`
}

// ProviderCredentials contains a JSON object with a user OAuth token.
type ProviderCredentials struct {
	// Source must be Secret. Credentials are re-read on each connection.
	// +kubebuilder:validation:Enum=Secret
	Source xpv2.CredentialsSource `json:"source"`
	// SecretRef selects the key holding {"token":"xoxp-..."}.
	// Namespaced ProviderConfig always resolves this in its own namespace.
	SecretRef *CredentialSecretReference `json:"secretRef"`
}

// CredentialSecretReference selects a credential key. Namespace is optional
// for ProviderConfig and mandatory for ClusterProviderConfig.
type CredentialSecretReference struct {
	// +kubebuilder:validation:MinLength=1
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// ProviderConfigStatus records usage and conditions.
type ProviderConfigStatus struct {
	xpv2.ProviderConfigStatus `json:",inline"`
}

// ProviderConfig configures Slack in the managed resource's namespace.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,provider,slack}
// +kubebuilder:storageversion
type ProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ProviderConfigSpec   `json:"spec"`
	Status            ProviderConfigStatus `json:"status,omitempty"`
}

// ProviderConfigList lists ProviderConfigs.
// +kubebuilder:object:root=true
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

// ClusterProviderConfig configures Slack using an explicitly namespaced Secret.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,provider,slack}
// +kubebuilder:storageversion
type ClusterProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="has(self.credentials.secretRef.namespace) && size(self.credentials.secretRef.namespace) > 0",message="ClusterProviderConfig requires an explicit credential Secret namespace"
	Spec   ProviderConfigSpec   `json:"spec"`
	Status ProviderConfigStatus `json:"status,omitempty"`
}

// ClusterProviderConfigList lists ClusterProviderConfigs.
// +kubebuilder:object:root=true
type ClusterProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProviderConfig `json:"items"`
}

// ProviderConfigUsage protects provider configuration while it is referenced.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,provider,slack}
type ProviderConfigUsage struct {
	metav1.TypeMeta               `json:",inline"`
	metav1.ObjectMeta             `json:"metadata,omitempty"`
	xpv2.TypedProviderConfigUsage `json:",inline"`
}

// ProviderConfigUsageList lists ProviderConfigUsages.
// +kubebuilder:object:root=true
type ProviderConfigUsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfigUsage `json:"items"`
}
