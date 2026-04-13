package controllers

import (
	"context"
	"fmt"
	"net/http"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-v1-secret,mutating=false,failurePolicy=fail,groups="",resources=secrets,verbs=update;delete,versions=v1,name=vsecret.kb.io,admissionReviewVersions=v1,sideEffects=None

// SecretValidator validates Secret operations
type SecretValidator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// +kubebuilder:webhook:path=/validate-v1-configmap,mutating=false,failurePolicy=fail,groups="",resources=configmaps,verbs=update;delete,versions=v1,name=vconfigmap.kb.io,admissionReviewVersions=v1,sideEffects=None

// ConfigMapValidator validates ConfigMap operations
type ConfigMapValidator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// Handle validates Secret update/delete operations
func (v *SecretValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Allow requests from the operator's service account
	if isOperatorServiceAccount(req.UserInfo) {
		return admission.Allowed("operator service account")
	}

	secret := &corev1.Secret{}

	// Only handle UPDATE and DELETE operations
	switch req.Operation {
	case "DELETE":
		// For DELETE operations, get the object from OldObject
		err := v.decoder.DecodeRaw(req.OldObject, secret)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	case "UPDATE":
		// For UPDATE operations, get the new object
		err := v.decoder.Decode(req, secret)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	default:
		// Should not reach here as webhook is configured for UPDATE and DELETE only
		return admission.Allowed("")
	}

	// Check if this is a synced secret
	syncedFrom, hasSyncedFrom := secret.Labels[ConfigSyncLabel]
	syncedBy, hasSyncedBy := secret.Labels[ConfigSyncNameLabel]

	// If both labels exist, this is a synced resource
	if hasSyncedFrom && hasSyncedBy {
		return admission.Denied(fmt.Sprintf(
			"Secret '%s' is managed by ConfigSync (synced-from: %s, synced-by: %s) and cannot be modified or deleted manually. "+
				"Please modify the source Secret in namespace '%s' instead.",
			secret.Name, syncedFrom, syncedBy, syncedFrom))
	}

	return admission.Allowed("")
}

// Handle validates ConfigMap update/delete operations
func (v *ConfigMapValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Allow requests from the operator's service account
	if isOperatorServiceAccount(req.UserInfo) {
		return admission.Allowed("operator service account")
	}

	configMap := &corev1.ConfigMap{}

	// Only handle UPDATE and DELETE operations
	switch req.Operation {
	case "DELETE":
		// For DELETE operations, get the object from OldObject
		err := v.decoder.DecodeRaw(req.OldObject, configMap)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	case "UPDATE":
		// For UPDATE operations, get the new object
		err := v.decoder.Decode(req, configMap)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	default:
		// Should not reach here as webhook is configured for UPDATE and DELETE only
		return admission.Allowed("")
	}

	// Check if this is a synced configmap
	syncedFrom, hasSyncedFrom := configMap.Labels[ConfigSyncLabel]
	syncedBy, hasSyncedBy := configMap.Labels[ConfigSyncNameLabel]

	// If both labels exist, this is a synced resource
	if hasSyncedFrom && hasSyncedBy {
		return admission.Denied(fmt.Sprintf(
			"ConfigMap '%s' is managed by ConfigSync (synced-from: %s, synced-by: %s) and cannot be modified or deleted manually. "+
				"Please modify the source ConfigMap in namespace '%s' instead.",
			configMap.Name, syncedFrom, syncedBy, syncedFrom))
	}

	return admission.Allowed("")
}

// isOperatorServiceAccount checks if the request is from the operator's service account
func isOperatorServiceAccount(userInfo authenticationv1.UserInfo) bool {
	// Check if the username matches the operator's service account pattern
	// Format: system:serviceaccount:<namespace>:<serviceaccount-name>
	username := userInfo.Username

	// Allow the operator's service account
	if username == "system:serviceaccount:config-sync-system:config-sync-operator" {
		return true
	}

	// Also allow cluster-admin and system components for operational needs
	for _, group := range userInfo.Groups {
		if group == "system:masters" {
			return true
		}
	}

	return false
} // InjectDecoder injects the decoder
func (v *SecretValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}

// InjectDecoder injects the decoder
func (v *ConfigMapValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}

// SetupWebhookWithManager sets up the webhook with the Manager
func SetupSecretWebhook(mgr ctrl.Manager) error {
	decoder := admission.NewDecoder(mgr.GetScheme())
	validator := &SecretValidator{
		Client:  mgr.GetClient(),
		decoder: decoder,
	}
	mgr.GetWebhookServer().Register("/validate-v1-secret", &webhook.Admission{
		Handler: validator,
	})
	return nil
}

// SetupWebhookWithManager sets up the webhook with the Manager
func SetupConfigMapWebhook(mgr ctrl.Manager) error {
	decoder := admission.NewDecoder(mgr.GetScheme())
	validator := &ConfigMapValidator{
		Client:  mgr.GetClient(),
		decoder: decoder,
	}
	mgr.GetWebhookServer().Register("/validate-v1-configmap", &webhook.Admission{
		Handler: validator,
	})
	return nil
}
