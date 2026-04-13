package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	configsyncv1 "config-sync-operator/api/v1"
)

// NamespaceReconciler watches namespace creation and triggers sync
type NamespaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the namespace
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, namespace); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip system namespaces
	if namespace.Name == "kube-system" || namespace.Name == "kube-public" || namespace.Name == "kube-node-lease" {
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("new namespace created, triggering ConfigSync reconciliation", "namespace", namespace.Name)

	// Trigger reconciliation of all ConfigSync resources that should sync to this namespace
	if err := r.triggerConfigSyncForNamespace(ctx, namespace.Name); err != nil {
		logger.Error(err, "failed to trigger ConfigSync reconciliation for namespace")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NamespaceReconciler) triggerConfigSyncForNamespace(ctx context.Context, namespaceName string) error {
	logger := log.FromContext(ctx)

	// List all ConfigSync resources
	configSyncList := &configsyncv1.ConfigSyncList{}
	if err := r.List(ctx, configSyncList); err != nil {
		return err
	}

	// For each ConfigSync, check if it should sync to this namespace
	for _, cs := range configSyncList.Items {
		shouldSync := false

		// Check if namespace is in target namespaces
		if len(cs.Spec.TargetNamespaces) > 0 {
			for _, targetNS := range cs.Spec.TargetNamespaces {
				if targetNS == namespaceName {
					shouldSync = true
					break
				}
			}
		}

		// Check if namespace is excluded
		if shouldSync {
			for _, excludedNS := range cs.Spec.ExcludedNamespaces {
				if excludedNS == namespaceName {
					shouldSync = false
					break
				}
			}
		}

		// If should sync, trigger reconciliation
		if shouldSync {
			if cs.Annotations == nil {
				cs.Annotations = make(map[string]string)
			}
			// 使用时间戳确保每次值都不同，这样才能触发 reconciliation
			cs.Annotations["config.zyql.com/last-trigger"] = "namespace-created-" + namespaceName + "-" + time.Now().Format(time.RFC3339Nano)
			if err := r.Update(ctx, &cs); err != nil {
				logger.Error(err, "failed to trigger ConfigSync reconciliation", "configsync", cs.Name, "namespace", namespaceName)
				return err
			}
			logger.V(1).Info("triggered ConfigSync reconciliation for new namespace", "configsync", cs.Name, "namespace", namespaceName)
		}
	}

	return nil
}

func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// 只处理创建事件
				ns := e.Object.(*corev1.Namespace)
				// 跳过系统 namespace
				if ns.Name == "kube-system" || ns.Name == "kube-public" || ns.Name == "kube-node-lease" {
					return false
				}
				return true
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// 忽略更新事件
				return false
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// 忽略删除事件
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				// 忽略通用事件
				return false
			},
		}).
		Complete(r)
}
