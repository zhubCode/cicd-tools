package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConfigSyncSpec defines the desired state of ConfigSync
type ConfigSyncSpec struct {
	// SourceNamespace is the namespace to sync from (default: operator's namespace)
	// +optional
	SourceNamespace string `json:"sourceNamespace,omitempty"`

	// TargetNamespaces is the list of namespaces to sync to
	// If empty, sync to all namespaces (except excluded ones)
	// +optional
	TargetNamespaces []string `json:"targetNamespaces,omitempty"`

	// ExcludedNamespaces is the list of namespaces to exclude from syncing
	// +optional
	ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`

	// SyncSecrets enables syncing of Secrets (default: true)
	// +optional
	SyncSecrets *bool `json:"syncSecrets,omitempty"`

	// SyncConfigMaps enables syncing of ConfigMaps (default: true)
	// +optional
	SyncConfigMaps *bool `json:"syncConfigMaps,omitempty"`

	// SecretSelector allows filtering which secrets to sync
	// +optional
	SecretSelector *metav1.LabelSelector `json:"secretSelector,omitempty"`

	// ConfigMapSelector allows filtering which configmaps to sync
	// +optional
	ConfigMapSelector *metav1.LabelSelector `json:"configMapSelector,omitempty"`

	// SyncIntervalSeconds is the interval in seconds between periodic syncs (default: 300, i.e. 5 minutes)
	// Minimum value is 60 seconds
	// +optional
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=60
	SyncIntervalSeconds *int `json:"syncIntervalSeconds,omitempty"`
}

// ConfigSyncStatus defines the observed state of ConfigSync
type ConfigSyncStatus struct {
	// SyncedSecrets is the count of synced secrets
	SyncedSecrets int `json:"syncedSecrets,omitempty"`

	// SyncedConfigMaps is the count of synced configmaps
	SyncedConfigMaps int `json:"syncedConfigMaps,omitempty"`

	// LastSyncTime is the timestamp of the last successful sync
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations of the ConfigSync's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TargetNamespaceStatus tracks sync status for each target namespace
	// +optional
	TargetNamespaceStatus []NamespaceStatus `json:"targetNamespaceStatus,omitempty"`
}

// NamespaceStatus represents the sync status of a single namespace
type NamespaceStatus struct {
	// Namespace is the name of the target namespace
	Namespace string `json:"namespace"`

	// SyncedSecrets is the count of secrets synced to this namespace
	SyncedSecrets int `json:"syncedSecrets"`

	// SyncedConfigMaps is the count of configmaps synced to this namespace
	SyncedConfigMaps int `json:"syncedConfigMaps"`

	// LastSyncTime is when this namespace was last synced
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Status indicates if the sync was successful
	Status string `json:"status"`

	// Message provides additional details
	// +optional
	Message string `json:"message,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// ConfigSync is the Schema for the configsyncs API
type ConfigSync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSyncSpec   `json:"spec,omitempty"`
	Status ConfigSyncStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConfigSyncList contains a list of ConfigSync
type ConfigSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfigSync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConfigSync{}, &ConfigSyncList{})
}
