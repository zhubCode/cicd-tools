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

// SecretReconciler watches secrets in source namespace and triggers sync
type SecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the secret
	secret := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Secret was deleted - clean up synced copies
			logger.Info("source secret deleted, cleaning up synced copies", "secret", req.Name, "namespace", req.Namespace)
			if err := r.deleteSyncedSecrets(ctx, req.Namespace, req.Name); err != nil {
				logger.Error(err, "failed to delete synced secrets")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if this is a synced secret (to avoid loops)
	if _, ok := secret.Labels[ConfigSyncLabel]; ok {
		return ctrl.Result{}, nil
	}

	// Skip service account tokens
	if secret.Type == corev1.SecretTypeServiceAccountToken {
		return ctrl.Result{}, nil
	}

	// Trigger reconciliation of all ConfigSync resources
	triggered, err := r.triggerConfigSyncReconciliation(ctx, secret.Namespace, secret.Name)
	if err != nil {
		logger.Error(err, "failed to trigger ConfigSync reconciliation")
		return ctrl.Result{}, err
	}

	if triggered {
		logger.V(1).Info("secret changed, triggering sync", "secret", secret.Name, "namespace", secret.Namespace)
	}

	return ctrl.Result{}, nil
}

func (r *SecretReconciler) triggerConfigSyncReconciliation(ctx context.Context, namespace, secretName string) (bool, error) {
	// Get the Secret to check its labels
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	// Skip service account tokens
	if secret.Type == corev1.SecretTypeServiceAccountToken {
		return false, nil
	}

	// List all ConfigSync resources
	configSyncList := &configsyncv1.ConfigSyncList{}
	if err := r.List(ctx, configSyncList); err != nil {
		return false, err
	}

	triggered := false
	logger := log.FromContext(ctx)

	// For each ConfigSync that watches this namespace, trigger reconciliation
	// This is done by updating an annotation
	for _, cs := range configSyncList.Items {
		sourceNS := cs.Spec.SourceNamespace
		if sourceNS == "" {
			sourceNS = "default" // or operator namespace
		}

		if sourceNS != namespace {
			continue
		}

		// Check if Secret matches the selector
		if cs.Spec.SecretSelector != nil && len(cs.Spec.SecretSelector.MatchLabels) > 0 {
			selector, err := metav1.LabelSelectorAsSelector(cs.Spec.SecretSelector)
			if err != nil {
				logger.Error(err, "invalid selector", "configsync", cs.Name)
				continue
			}
			if !selector.Matches(labels.Set(secret.Labels)) {
				continue
			}
		}

		// This ConfigSync watches this Secret, update it to trigger reconciliation
		if cs.Annotations == nil {
			cs.Annotations = make(map[string]string)
		}
		// 使用时间戳确保每次值都不同，这样才能触发 reconciliation
		cs.Annotations["config.zyql.com/last-trigger"] = "secret-" + secretName + "-" + time.Now().Format(time.RFC3339Nano)
		if err := r.Update(ctx, &cs); err != nil {
			return false, err
		}
		logger.V(1).Info("triggered ConfigSync reconciliation", "configsync", cs.Name, "namespace", namespace, "secret", secretName)
		triggered = true
	}

	return triggered, nil
}

// deleteSyncedSecrets deletes all synced copies of a source Secret
func (r *SecretReconciler) deleteSyncedSecrets(ctx context.Context, sourceNamespace, secretName string) error {
	logger := log.FromContext(ctx)

	// List all secrets across all namespaces with the sync label
	secretList := &corev1.SecretList{}
	if err := r.List(ctx, secretList); err != nil {
		return err
	}

	for _, secret := range secretList.Items {
		// Check if this secret was synced from the source
		syncedFrom, hasSyncedFrom := secret.Labels[ConfigSyncLabel]

		if hasSyncedFrom && syncedFrom == sourceNamespace && secret.Name == secretName {
			logger.Info("deleting synced secret",
				"secret", secret.Name,
				"namespace", secret.Namespace,
				"sourceNamespace", sourceNamespace)

			if err := r.Delete(ctx, &secret); err != nil {
				if client.IgnoreNotFound(err) != nil {
					logger.Error(err, "failed to delete synced secret",
						"secret", secret.Name,
						"namespace", secret.Namespace)
				}
			}
		}
	}

	return nil
}

// matchesAnyConfigSync checks if a Secret matches any ConfigSync's selector
func (r *SecretReconciler) matchesAnyConfigSync(secret *corev1.Secret) bool {
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

		// Check if Secret is in the source namespace
		if secret.Namespace != sourceNS {
			continue
		}

		// Skip service account tokens
		if secret.Type == corev1.SecretTypeServiceAccountToken {
			continue
		}

		// If no selector specified, match all Secrets in source namespace
		if cs.Spec.SecretSelector == nil || len(cs.Spec.SecretSelector.MatchLabels) == 0 {
			return true
		}

		// Check if Secret labels match the selector
		selector, err := metav1.LabelSelectorAsSelector(cs.Spec.SecretSelector)
		if err != nil {
			continue
		}

		if selector.Matches(labels.Set(secret.Labels)) {
			return true
		}
	}

	return false
}

func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// 检查新创建的 Secret 是否满足任何 ConfigSync 的选择器要求
				secret := e.Object.(*corev1.Secret)

				// 跳过 service account tokens
				if secret.Type == corev1.SecretTypeServiceAccountToken {
					return false
				}

				// 如果是已同步的 Secret，忽略（避免循环）
				if _, ok := secret.Labels[ConfigSyncLabel]; ok {
					return false
				}

				// 检查是否有 ConfigSync 需要同步这个 Secret
				return r.matchesAnyConfigSync(secret)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// 只处理更新事件
				oldSecret := e.ObjectOld.(*corev1.Secret)
				newSecret := e.ObjectNew.(*corev1.Secret)

				// 跳过 service account tokens
				if newSecret.Type == corev1.SecretTypeServiceAccountToken {
					return false
				}

				// 如果是已同步的 Secret，忽略（避免循环）
				if _, ok := newSecret.Labels[ConfigSyncLabel]; ok {
					return false
				}

				// 只在 Data 发生变化时触发
				return !secretDataEqual(oldSecret.Data, newSecret.Data)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// 处理源 Secret 删除，需要删除所有同步的副本
				secret := e.Object.(*corev1.Secret)

				// 跳过 service account tokens
				if secret.Type == corev1.SecretTypeServiceAccountToken {
					return false
				}

				// 如果是被同步的 Secret，忽略
				if _, ok := secret.Labels[ConfigSyncLabel]; ok {
					return false
				}

				// 源 Secret 被删除，需要处理
				return true
			},
			GenericFunc: func(e event.GenericEvent) bool {
				// 忽略通用事件
				return false
			},
		}).
		Complete(r)
}

// secretDataEqual compares two secret data maps
func secretDataEqual(a, b map[string][]byte) bool {
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
