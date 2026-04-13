package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	configsyncv1 "config-sync-operator/api/v1"
)

// ConfigMapReconciler watches configmaps in source namespace and triggers sync
type ConfigMapReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the configmap
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, configMap); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ConfigMap was deleted - clean up synced copies
			logger.Info("source configmap deleted, cleaning up synced copies", "configmap", req.Name, "namespace", req.Namespace)
			if err := r.deleteSyncedConfigMaps(ctx, req.Namespace, req.Name); err != nil {
				logger.Error(err, "failed to delete synced configmaps")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if this is a synced configmap (to avoid loops)
	if _, ok := configMap.Labels[ConfigSyncLabel]; ok {
		return ctrl.Result{}, nil
	}

	// Trigger reconciliation of all ConfigSync resources
	triggered, err := r.triggerConfigSyncReconciliation(ctx, configMap.Namespace, configMap.Name)
	if err != nil {
		logger.Error(err, "failed to trigger ConfigSync reconciliation")
		return ctrl.Result{}, err
	}

	if triggered {
		logger.V(1).Info("configmap changed, triggering sync", "configmap", configMap.Name, "namespace", configMap.Namespace)
	}

	return ctrl.Result{}, nil
}

func (r *ConfigMapReconciler) triggerConfigSyncReconciliation(ctx context.Context, namespace, configMapName string) (bool, error) {
	// Get the ConfigMap to check its labels
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: configMapName}, configMap); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	// List all ConfigSync resources
	configSyncList := &configsyncv1.ConfigSyncList{}
	if err := r.List(ctx, configSyncList); err != nil {
		return false, err
	}

	triggered := false
	logger := log.FromContext(ctx)

	// For each ConfigSync that watches this namespace, trigger reconciliation
	for _, cs := range configSyncList.Items {
		sourceNS := cs.Spec.SourceNamespace
		if sourceNS == "" {
			sourceNS = "default" // or operator namespace
		}

		if sourceNS != namespace {
			continue
		}

		// Check if ConfigMap matches the selector
		if cs.Spec.ConfigMapSelector != nil && len(cs.Spec.ConfigMapSelector.MatchLabels) > 0 {
			selector, err := metav1.LabelSelectorAsSelector(cs.Spec.ConfigMapSelector)
			if err != nil {
				logger.Error(err, "invalid selector", "configsync", cs.Name)
				continue
			}
			if !selector.Matches(labels.Set(configMap.Labels)) {
				continue
			}
		}

		// This ConfigSync watches this ConfigMap, update it to trigger reconciliation
		if cs.Annotations == nil {
			cs.Annotations = make(map[string]string)
		}
		// 使用时间戳确保每次值都不同，这样才能触发 reconciliation
		cs.Annotations["config.zyql.com/last-trigger"] = "configmap-" + configMapName + "-" + time.Now().Format(time.RFC3339Nano)
		if err := r.Update(ctx, &cs); err != nil {
			return false, err
		}
		logger.V(1).Info("triggered ConfigSync reconciliation", "configsync", cs.Name, "namespace", namespace, "configmap", configMapName)
		triggered = true
	}

	return triggered, nil
}

// deleteSyncedConfigMaps deletes all synced copies of a source ConfigMap
func (r *ConfigMapReconciler) deleteSyncedConfigMaps(ctx context.Context, sourceNamespace, configMapName string) error {
	logger := log.FromContext(ctx)

	// List all configmaps across all namespaces with the sync label
	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList); err != nil {
		return err
	}

	for _, cm := range configMapList.Items {
		// Check if this configmap was synced from the source
		syncedFrom, hasSyncedFrom := cm.Labels[ConfigSyncLabel]

		if hasSyncedFrom && syncedFrom == sourceNamespace && cm.Name == configMapName {
			logger.Info("deleting synced configmap",
				"configmap", cm.Name,
				"namespace", cm.Namespace,
				"sourceNamespace", sourceNamespace)

			if err := r.Delete(ctx, &cm); err != nil {
				if client.IgnoreNotFound(err) != nil {
					logger.Error(err, "failed to delete synced configmap",
						"configmap", cm.Name,
						"namespace", cm.Namespace)
				}
			}
		}
	}

	return nil
}

// matchesAnyConfigSync checks if a ConfigMap matches any ConfigSync's selector
func (r *ConfigMapReconciler) matchesAnyConfigSync(cm *corev1.ConfigMap) bool {
	ctx := context.Background()

	// List all ConfigSync resources
	configSyncList := &configsyncv1.ConfigSyncList{}
	if err := r.List(ctx, configSyncList); err != nil {
		return false
	}

	// Check each ConfigSync
	for _, cs := range configSyncList.Items {
		sourceNS := cs.Spec.SourceNamespace
		if sourceNS == "" {
			sourceNS = "default"
		}

		// Check if ConfigMap is in the source namespace
		if cm.Namespace != sourceNS {
			continue
		}

		// If no selector specified, match all ConfigMaps in source namespace
		if cs.Spec.ConfigMapSelector == nil || len(cs.Spec.ConfigMapSelector.MatchLabels) == 0 {
			return true
		}

		// Check if ConfigMap labels match the selector
		selector, err := metav1.LabelSelectorAsSelector(cs.Spec.ConfigMapSelector)
		if err != nil {
			continue
		}

		if selector.Matches(labels.Set(cm.Labels)) {
			return true
		}
	}

	return false
}

func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// 检查新创建的 ConfigMap 是否满足任何 ConfigSync 的选择器要求
				cm := e.Object.(*corev1.ConfigMap)

				// 如果是已同步的 ConfigMap，忽略（避免循环）
				if _, ok := cm.Labels[ConfigSyncLabel]; ok {
					return false
				}

				// 检查是否有 ConfigSync 需要同步这个 ConfigMap
				return r.matchesAnyConfigSync(cm)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// 只处理更新事件
				oldCM := e.ObjectOld.(*corev1.ConfigMap)
				newCM := e.ObjectNew.(*corev1.ConfigMap)

				// 如果是已同步的 ConfigMap，忽略（避免循环）
				if _, ok := newCM.Labels[ConfigSyncLabel]; ok {
					return false
				}

				// 只在 Data 或 BinaryData 发生变化时触发
				return !dataEqual(oldCM.Data, newCM.Data) || !binaryDataEqual(oldCM.BinaryData, newCM.BinaryData)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// 处理源 ConfigMap 删除，需要删除所有同步的副本
				cm := e.Object.(*corev1.ConfigMap)

				// 如果是被同步的 ConfigMap，忽略
				if _, ok := cm.Labels[ConfigSyncLabel]; ok {
					return false
				}

				// 源 ConfigMap 被删除，需要处理
				return true
			},
			GenericFunc: func(e event.GenericEvent) bool {
				// 忽略通用事件
				return false
			},
		}).
		Complete(r)
}

// dataEqual compares two data maps
func dataEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// binaryDataEqual compares two binary data maps
func binaryDataEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if len(v) != len(bv) {
			return false
		}
		for i := range v {
			if v[i] != bv[i] {
				return false
			}
		}
	}
	return true
}
