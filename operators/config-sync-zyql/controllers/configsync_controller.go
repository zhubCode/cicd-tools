package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	configsyncv1 "config-sync-operator/api/v1"
)

const (
	ConfigSyncLabel      = "config.zyql.com/synced-from"
	ConfigSyncNameLabel  = "config.zyql.com/synced-by"
	ConfigSyncAnnotation = "config.zyql.com/source-namespace"
	ConfigSyncFinalizer  = "config.zyql.com/finalizer"
)

// ConfigSyncReconciler reconciles a ConfigSync object
type ConfigSyncReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=config.zyql.com,resources=configsyncs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=config.zyql.com,resources=configsyncs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=config.zyql.com,resources=configsyncs/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *ConfigSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ConfigSync instance
	configSync := &configsyncv1.ConfigSync{}
	if err := r.Get(ctx, req.NamespacedName, configSync); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch ConfigSync")
		return ctrl.Result{}, err
	}

	// Check if the ConfigSync is being deleted
	if !configSync.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is being deleted
		if containsString(configSync.ObjectMeta.Finalizers, ConfigSyncFinalizer) {
			// Our finalizer is present, cleanup synced resources
			if err := r.cleanupSyncedResources(ctx, configSync); err != nil {
				logger.Error(err, "failed to cleanup synced resources")
				return ctrl.Result{}, err
			}

			// Remove our finalizer
			configSync.ObjectMeta.Finalizers = removeString(configSync.ObjectMeta.Finalizers, ConfigSyncFinalizer)
			if err := r.Update(ctx, configSync); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("cleaned up synced resources and removed finalizer", "configsync", configSync.Name)
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if it doesn't exist
	if !containsString(configSync.ObjectMeta.Finalizers, ConfigSyncFinalizer) {
		configSync.ObjectMeta.Finalizers = append(configSync.ObjectMeta.Finalizers, ConfigSyncFinalizer)
		if err := r.Update(ctx, configSync); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("added finalizer", "configsync", configSync.Name)
		return ctrl.Result{Requeue: true}, nil
	}

	// Determine source namespace
	sourceNamespace := configSync.Spec.SourceNamespace
	if sourceNamespace == "" {
		// Get operator's namespace from environment or use default
		sourceNamespace = "default"
		if ns := getOperatorNamespace(); ns != "" {
			sourceNamespace = ns
		}
	}

	// Get target namespaces
	targetNamespaces, err := r.getTargetNamespaces(ctx, configSync, sourceNamespace)
	if err != nil {
		logger.Error(err, "failed to get target namespaces")
		return ctrl.Result{}, err
	}

	// Clean up resources that were previously synced into namespaces that are no longer targets
	if err := r.cleanupRemovedNamespaces(ctx, configSync, sourceNamespace, targetNamespaces); err != nil {
		logger.Error(err, "failed to cleanup resources in removed namespaces")
		return ctrl.Result{}, err
	}

	// Initialize status
	var namespaceStatuses []configsyncv1.NamespaceStatus
	totalSecrets := 0
	totalConfigMaps := 0

	// Sync secrets if enabled
	syncSecrets := true
	if configSync.Spec.SyncSecrets != nil {
		syncSecrets = *configSync.Spec.SyncSecrets
	}

	// Sync configmaps if enabled
	syncConfigMaps := true
	if configSync.Spec.SyncConfigMaps != nil {
		syncConfigMaps = *configSync.Spec.SyncConfigMaps
	}

	// Process each target namespace
	for _, targetNS := range targetNamespaces {
		nsStatus := configsyncv1.NamespaceStatus{
			Namespace: targetNS,
			Status:    "Success",
		}

		// Sync Secrets
		if syncSecrets {
			secrets, err := r.listSourceSecrets(ctx, sourceNamespace, configSync.Spec.SecretSelector)
			if err != nil {
				logger.Error(err, "failed to list secrets", "namespace", sourceNamespace)
				nsStatus.Status = "Failed"
				nsStatus.Message = fmt.Sprintf("failed to list secrets: %v", err)
			} else {
				syncedCount := 0
				for _, secret := range secrets {
					if err := r.syncSecret(ctx, &secret, targetNS, sourceNamespace, configSync.Name); err != nil {
						logger.Error(err, "failed to sync secret", "secret", secret.Name, "target", targetNS)
					} else {
						syncedCount++
					}
				}
				nsStatus.SyncedSecrets = syncedCount
				totalSecrets += syncedCount
			}
		}

		// Sync ConfigMaps
		if syncConfigMaps {
			configMaps, err := r.listSourceConfigMaps(ctx, sourceNamespace, configSync.Spec.ConfigMapSelector)
			if err != nil {
				logger.Error(err, "failed to list configmaps", "namespace", sourceNamespace)
				nsStatus.Status = "Failed"
				nsStatus.Message = fmt.Sprintf("failed to list configmaps: %v", err)
			} else {
				syncedCount := 0
				for _, cm := range configMaps {
					if err := r.syncConfigMap(ctx, &cm, targetNS, sourceNamespace, configSync.Name); err != nil {
						logger.Error(err, "failed to sync configmap", "configmap", cm.Name, "target", targetNS)
					} else {
						syncedCount++
					}
				}
				nsStatus.SyncedConfigMaps = syncedCount
				totalConfigMaps += syncedCount
			}
		}

		now := metav1.Now()
		nsStatus.LastSyncTime = &now
		namespaceStatuses = append(namespaceStatuses, nsStatus)
	}

	// Update status
	now := metav1.Now()
	configSync.Status.SyncedSecrets = totalSecrets
	configSync.Status.SyncedConfigMaps = totalConfigMaps
	configSync.Status.LastSyncTime = &now
	configSync.Status.TargetNamespaceStatus = namespaceStatuses

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "SyncSuccessful",
		Message:            fmt.Sprintf("Synced %d secrets and %d configmaps to %d namespaces", totalSecrets, totalConfigMaps, len(targetNamespaces)),
		LastTransitionTime: now,
	}
	configSync.Status.Conditions = []metav1.Condition{condition}

	if err := r.Status().Update(ctx, configSync); err != nil {
		logger.Error(err, "failed to update ConfigSync status")
		return ctrl.Result{}, err
	}

	// Requeue after configured interval for periodic sync
	syncInterval := r.getSyncInterval(configSync)
	logger.V(1).Info("scheduling next sync", "interval", syncInterval)
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func (r *ConfigSyncReconciler) getTargetNamespaces(ctx context.Context, configSync *configsyncv1.ConfigSync, sourceNS string) ([]string, error) {
	// If target namespaces are explicitly specified, use them
	if len(configSync.Spec.TargetNamespaces) > 0 {
		return r.filterNamespaces(configSync.Spec.TargetNamespaces, configSync.Spec.ExcludedNamespaces, sourceNS), nil
	}

	// Otherwise, get all namespaces
	namespaceList := &corev1.NamespaceList{}
	if err := r.List(ctx, namespaceList); err != nil {
		return nil, err
	}

	var allNamespaces []string
	for _, ns := range namespaceList.Items {
		allNamespaces = append(allNamespaces, ns.Name)
	}

	return r.filterNamespaces(allNamespaces, configSync.Spec.ExcludedNamespaces, sourceNS), nil
}

func (r *ConfigSyncReconciler) filterNamespaces(namespaces, excludedNamespaces []string, sourceNS string) []string {
	excludedSet := make(map[string]bool)
	excludedSet[sourceNS] = true // Always exclude source namespace
	excludedSet["kube-system"] = true
	excludedSet["kube-public"] = true
	excludedSet["kube-node-lease"] = true

	for _, ns := range excludedNamespaces {
		excludedSet[ns] = true
	}

	var result []string
	for _, ns := range namespaces {
		if !excludedSet[ns] {
			result = append(result, ns)
		}
	}
	return result
}

func (r *ConfigSyncReconciler) listSourceSecrets(ctx context.Context, namespace string, selector *metav1.LabelSelector) ([]corev1.Secret, error) {
	secretList := &corev1.SecretList{}
	listOpts := &client.ListOptions{
		Namespace: namespace,
	}

	if selector != nil {
		labelSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return nil, err
		}
		listOpts.LabelSelector = labelSelector
	}

	if err := r.List(ctx, secretList, listOpts); err != nil {
		return nil, err
	}

	// Filter out service account tokens and synced secrets
	var filtered []corev1.Secret
	for _, secret := range secretList.Items {
		if secret.Type == corev1.SecretTypeServiceAccountToken {
			continue
		}
		if _, ok := secret.Labels[ConfigSyncLabel]; ok {
			continue // Skip already synced secrets
		}
		filtered = append(filtered, secret)
	}

	return filtered, nil
}

func (r *ConfigSyncReconciler) listSourceConfigMaps(ctx context.Context, namespace string, selector *metav1.LabelSelector) ([]corev1.ConfigMap, error) {
	configMapList := &corev1.ConfigMapList{}
	listOpts := &client.ListOptions{
		Namespace: namespace,
	}

	if selector != nil {
		labelSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return nil, err
		}
		listOpts.LabelSelector = labelSelector
	}

	if err := r.List(ctx, configMapList, listOpts); err != nil {
		return nil, err
	}

	// Filter out synced configmaps
	var filtered []corev1.ConfigMap
	for _, cm := range configMapList.Items {
		if _, ok := cm.Labels[ConfigSyncLabel]; ok {
			continue // Skip already synced configmaps
		}
		filtered = append(filtered, cm)
	}

	return filtered, nil
}

func (r *ConfigSyncReconciler) syncSecret(ctx context.Context, source *corev1.Secret, targetNS, sourceNS, configSyncName string) error {
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        source.Name,
			Namespace:   targetNS,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
		Type: source.Type,
		Data: source.Data,
	}

	// Copy labels
	for k, v := range source.Labels {
		target.Labels[k] = v
	}
	target.Labels[ConfigSyncLabel] = sourceNS
	target.Labels[ConfigSyncNameLabel] = configSyncName

	// Copy annotations
	for k, v := range source.Annotations {
		target.Annotations[k] = v
	}
	target.Annotations[ConfigSyncAnnotation] = sourceNS

	// Try to get existing secret
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: target.Name, Namespace: targetNS}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			return r.Create(ctx, target)
		}
		return err
	}

	// Update existing secret if it's managed by us
	if _, ok := existing.Labels[ConfigSyncLabel]; ok {
		existing.Data = target.Data
		existing.Labels = target.Labels
		existing.Annotations = target.Annotations
		existing.Type = target.Type
		return r.Update(ctx, existing)
	}

	// Secret exists but not managed by us, skip
	return nil
}

func (r *ConfigSyncReconciler) syncConfigMap(ctx context.Context, source *corev1.ConfigMap, targetNS, sourceNS, configSyncName string) error {
	target := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        source.Name,
			Namespace:   targetNS,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
		Data:       source.Data,
		BinaryData: source.BinaryData,
	}

	// Copy labels
	for k, v := range source.Labels {
		target.Labels[k] = v
	}
	target.Labels[ConfigSyncLabel] = sourceNS
	target.Labels[ConfigSyncNameLabel] = configSyncName

	// Copy annotations
	for k, v := range source.Annotations {
		target.Annotations[k] = v
	}
	target.Annotations[ConfigSyncAnnotation] = sourceNS

	// Try to get existing configmap
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: target.Name, Namespace: targetNS}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new configmap
			return r.Create(ctx, target)
		}
		return err
	}

	// Update existing configmap if it's managed by us
	if _, ok := existing.Labels[ConfigSyncLabel]; ok {
		existing.Data = target.Data
		existing.BinaryData = target.BinaryData
		existing.Labels = target.Labels
		existing.Annotations = target.Annotations
		return r.Update(ctx, existing)
	}

	// ConfigMap exists but not managed by us, skip
	return nil
}

func (r *ConfigSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configsyncv1.ConfigSync{}).
		Complete(r)
}

func getOperatorNamespace() string {
	// Try to read from downward API or environment variable
	// This would be set in the deployment
	return ""
}

// cleanupSyncedResources deletes all resources that were synced by this ConfigSync
func (r *ConfigSyncReconciler) cleanupSyncedResources(ctx context.Context, configSync *configsyncv1.ConfigSync) error {
	logger := log.FromContext(ctx)

	// Determine source namespace
	sourceNamespace := configSync.Spec.SourceNamespace
	if sourceNamespace == "" {
		sourceNamespace = "default"
		if ns := getOperatorNamespace(); ns != "" {
			sourceNamespace = ns
		}
	}

	// Get target namespaces
	targetNamespaces, err := r.getTargetNamespaces(ctx, configSync, sourceNamespace)
	if err != nil {
		return fmt.Errorf("failed to get target namespaces: %w", err)
	}

	logger.Info("cleaning up synced resources", "configsync", configSync.Name, "targetNamespaces", len(targetNamespaces))

	// Delete synced secrets
	syncSecrets := true
	if configSync.Spec.SyncSecrets != nil {
		syncSecrets = *configSync.Spec.SyncSecrets
	}

	if syncSecrets {
		for _, targetNS := range targetNamespaces {
			secretList := &corev1.SecretList{}
			listOpts := &client.ListOptions{
				Namespace: targetNS,
			}

			if err := r.List(ctx, secretList, listOpts); err != nil {
				logger.Error(err, "failed to list secrets for cleanup", "namespace", targetNS)
				continue
			}

			for _, secret := range secretList.Items {
				// Check if this secret was synced by THIS ConfigSync from this source namespace
				syncedFrom, hasSyncedFrom := secret.Labels[ConfigSyncLabel]
				syncedBy, hasSyncedBy := secret.Labels[ConfigSyncNameLabel]

				if hasSyncedFrom && hasSyncedBy && syncedFrom == sourceNamespace && syncedBy == configSync.Name {
					logger.Info("deleting synced secret", "secret", secret.Name, "namespace", targetNS, "configsync", configSync.Name)
					if err := r.Delete(ctx, &secret); err != nil && !errors.IsNotFound(err) {
						logger.Error(err, "failed to delete synced secret", "secret", secret.Name, "namespace", targetNS)
					}
				}
			}
		}
	}

	// Delete synced configmaps
	syncConfigMaps := true
	if configSync.Spec.SyncConfigMaps != nil {
		syncConfigMaps = *configSync.Spec.SyncConfigMaps
	}

	if syncConfigMaps {
		for _, targetNS := range targetNamespaces {
			configMapList := &corev1.ConfigMapList{}
			listOpts := &client.ListOptions{
				Namespace: targetNS,
			}

			if err := r.List(ctx, configMapList, listOpts); err != nil {
				logger.Error(err, "failed to list configmaps for cleanup", "namespace", targetNS)
				continue
			}

			for _, cm := range configMapList.Items {
				// Check if this configmap was synced by THIS ConfigSync from this source namespace
				syncedFrom, hasSyncedFrom := cm.Labels[ConfigSyncLabel]
				syncedBy, hasSyncedBy := cm.Labels[ConfigSyncNameLabel]

				if hasSyncedFrom && hasSyncedBy && syncedFrom == sourceNamespace && syncedBy == configSync.Name {
					logger.Info("deleting synced configmap", "configmap", cm.Name, "namespace", targetNS, "configsync", configSync.Name)
					if err := r.Delete(ctx, &cm); err != nil && !errors.IsNotFound(err) {
						logger.Error(err, "failed to delete synced configmap", "configmap", cm.Name, "namespace", targetNS)
					}
				}
			}
		}
	}

	logger.Info("cleanup completed", "configsync", configSync.Name)
	return nil
}

// getSyncInterval returns the sync interval duration from the ConfigSync spec
// If not specified, defaults to 5 minutes
func (r *ConfigSyncReconciler) getSyncInterval(configSync *configsyncv1.ConfigSync) time.Duration {
	if configSync.Spec.SyncIntervalSeconds != nil {
		seconds := *configSync.Spec.SyncIntervalSeconds
		// Enforce minimum of 60 seconds
		if seconds < 60 {
			seconds = 60
		}
		return time.Duration(seconds) * time.Second
	}
	// Default to 5 minutes
	return 5 * time.Minute
}

// cleanupRemovedNamespaces deletes synced resources that exist in namespaces which are no longer targeted
func (r *ConfigSyncReconciler) cleanupRemovedNamespaces(ctx context.Context, configSync *configsyncv1.ConfigSync, sourceNamespace string, currentTargets []string) error {
	logger := log.FromContext(ctx)

	// Build a set of current target namespaces for quick lookup
	targetSet := make(map[string]bool)
	for _, ns := range currentTargets {
		targetSet[ns] = true
	}

	// helper to decide whether to delete from a namespace
	shouldDeleteFrom := func(ns string) bool {
		// never delete from source or kube-system-ish namespaces
		if ns == sourceNamespace {
			return false
		}
		if ns == "kube-system" || ns == "kube-public" || ns == "kube-node-lease" {
			return false
		}
		// if not in current targets, we should delete any previously-synced resources
		if _, ok := targetSet[ns]; !ok {
			return true
		}
		return false
	}

	// Delete synced Secrets in namespaces that are no longer targets
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList); err != nil {
		logger.Error(err, "failed to list secrets for removed-namespaces cleanup")
		return err
	}

	for _, secret := range secretList.Items {
		// only consider secrets managed by this ConfigSync
		syncedFrom, hasSyncedFrom := secret.Labels[ConfigSyncLabel]
		syncedBy, hasSyncedBy := secret.Labels[ConfigSyncNameLabel]
		if !hasSyncedFrom || !hasSyncedBy {
			continue
		}
		if syncedFrom != sourceNamespace || syncedBy != configSync.Name {
			continue
		}

		if shouldDeleteFrom(secret.Namespace) {
			logger.Info("deleting previously-synced secret in removed namespace", "secret", secret.Name, "namespace", secret.Namespace)
			if err := r.Delete(ctx, &secret); err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "failed to delete secret in removed namespace", "secret", secret.Name, "namespace", secret.Namespace)
			}
		}
	}

	// Delete synced ConfigMaps in namespaces that are no longer targets
	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList); err != nil {
		logger.Error(err, "failed to list configmaps for removed-namespaces cleanup")
		return err
	}

	for _, cm := range configMapList.Items {
		syncedFrom, hasSyncedFrom := cm.Labels[ConfigSyncLabel]
		syncedBy, hasSyncedBy := cm.Labels[ConfigSyncNameLabel]
		if !hasSyncedFrom || !hasSyncedBy {
			continue
		}
		if syncedFrom != sourceNamespace || syncedBy != configSync.Name {
			continue
		}

		if shouldDeleteFrom(cm.Namespace) {
			logger.Info("deleting previously-synced configmap in removed namespace", "configmap", cm.Name, "namespace", cm.Namespace)
			if err := r.Delete(ctx, &cm); err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "failed to delete configmap in removed namespace", "configmap", cm.Name, "namespace", cm.Namespace)
			}
		}
	}

	return nil
}

// Helper function to check if a slice contains a string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// Helper function to remove a string from a slice
func removeString(slice []string, s string) []string {
	result := []string{}
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
