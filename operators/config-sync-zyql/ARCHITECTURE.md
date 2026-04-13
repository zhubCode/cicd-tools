# 系统架构说明

## 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐         ┌────────────────────────────┐       │
│  │   ConfigSync │────────▶│   ConfigSyncReconciler    │       │
│  │     (CRD)    │         │  (Main Controller)         │       │
│  └──────────────┘         │                            │       │
│                           │  - 列举源资源              │       │
│                           │  - 过滤和选择              │       │
│                           │  - 同步到目标 namespace   │       │
│                           │  - 更新状态                │       │
│                           │  - 5分钟定期同步           │       │
│                           └────────────────────────────┘       │
│                                      │                          │
│                                      │ watches                  │
│                                      ▼                          │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              Source Namespace (default)                 │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐        │   │
│  │  │   Secret   │  │   Secret   │  │ ConfigMap  │        │   │
│  │  │    app1    │  │    app2    │  │  app-cfg   │        │   │
│  │  └────────────┘  └────────────┘  └────────────┘        │   │
│  └─────────────────────────────────────────────────────────┘   │
│         ▲                   ▲                  ▲               │
│         │                   │                  │               │
│         │                   │                  │               │
│  ┌──────┴──────┐     ┌──────┴──────┐   ┌──────┴──────┐       │
│  │   Secret    │     │   Secret    │   │  ConfigMap  │       │
│  │  Reconciler │     │  Reconciler │   │  Reconciler │       │
│  │             │     │             │   │             │       │
│  │  Watches    │     │  Watches    │   │  Watches    │       │
│  │  CREATE     │     │  UPDATE     │   │  CREATE     │       │
│  │  UPDATE     │     │  DELETE     │   │  UPDATE     │       │
│  └─────────────┘     └─────────────┘   └─────────────┘       │
│         │                   │                  │               │
│         │                   │                  │               │
│         └───────────────────┴──────────────────┘               │
│                             │                                  │
│                    triggers │ immediate sync                   │
│                             ▼                                  │
│                   ┌────────────────────┐                       │
│                   │ ConfigSync Update  │                       │
│                   │ (add annotation)   │                       │
│                   └────────────────────┘                       │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │           Target Namespaces (dev, staging, prod)        │   │
│  │                                                          │   │
│  │  Namespace: dev                                          │   │
│  │  ┌────────────────────────────────────────────────┐     │   │
│  │  │ Secret: app1                                   │     │   │
│  │  │ Labels:                                        │     │   │
│  │  │   config.zyql.com/synced-from: default     │     │   │
│  │  │   config.zyql.com/synced-by: example-sync  │     │   │
│  │  └────────────────────────────────────────────────┘     │   │
│  │                                                          │   │
│  │  Namespace: staging                                      │   │
│  │  ┌────────────────────────────────────────────────┐     │   │
│  │  │ ConfigMap: app-cfg                             │     │   │
│  │  │ Labels:                                        │     │   │
│  │  │   config.zyql.com/synced-from: default     │     │   │
│  │  │   config.zyql.com/synced-by: example-sync  │     │   │
│  │  └────────────────────────────────────────────────┘     │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Webhook 保护机制

```
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes API Server                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  User/Process attempts to modify synced resource:              │
│                                                                 │
│  kubectl edit configmap app-cfg -n dev                          │
│         │                                                       │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │   Admission Controller                     │                │
│  │                                             │                │
│  │   1. Check ValidatingWebhookConfiguration  │                │
│  │   2. Match resource (ConfigMap/Secret)     │                │
│  │   3. Match operation (UPDATE/DELETE)       │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         │ HTTPS Request                                        │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │   Config Sync Operator                     │                │
│  │   Webhook Server (Port 9443)               │                │
│  │                                             │                │
│  │   ┌─────────────────────────────────────┐  │                │
│  │   │  SecretValidator.Handle()           │  │                │
│  │   │                                     │  │                │
│  │   │  1. Check labels:                   │  │                │
│  │   │     - synced-from                   │  │                │
│  │   │     - synced-by                     │  │                │
│  │   │                                     │  │                │
│  │   │  2. If both exist:                  │  │                │
│  │   │     ❌ admission.Denied()           │  │                │
│  │   │     "managed by ConfigSync"         │  │                │
│  │   │                                     │  │                │
│  │   │  3. Otherwise:                      │  │                │
│  │   │     ✅ admission.Allowed()          │  │                │
│  │   └─────────────────────────────────────┘  │                │
│  │                                             │                │
│  │   ┌─────────────────────────────────────┐  │                │
│  │   │  ConfigMapValidator.Handle()        │  │                │
│  │   │  (same logic as Secret)             │  │                │
│  │   └─────────────────────────────────────┘  │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         │ Response                                             │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │   Admission Decision                       │                │
│  │                                             │                │
│  │   ✅ Allowed: Apply change                 │                │
│  │   ❌ Denied: Reject with error message     │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         ▼                                                       │
│  Return to User:                                               │
│  Error: admission webhook "vsecret.kb.io" denied the request:  │
│  Secret 'app1' is managed by ConfigSync 'example-sync'...      │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 自动恢复机制

```
┌─────────────────────────────────────────────────────────────────┐
│                   Scenario: Webhook Disabled                    │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Time 0: Normal State                                           │
│  ┌────────────────────────────────────────────┐                │
│  │ dev/app-cfg: {"key": "value"}              │                │
│  │ Labels: synced-from, synced-by             │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
│  Time 1: Manual Modification (webhook disabled)                │
│  ┌────────────────────────────────────────────┐                │
│  │ kubectl patch configmap app-cfg            │                │
│  │   -n dev -p '{"data":{"key":"hacked"}}'    │                │
│  │                                             │                │
│  │ ✅ SUCCESS (no webhook to block)           │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │ dev/app-cfg: {"key": "hacked"}  ⚠️         │                │
│  │ Labels: synced-from, synced-by             │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
│  Time 2-5: Wait (max 5 minutes)                                │
│  ⏰ Periodic reconciliation interval                            │
│                                                                 │
│  Time 5: Automatic Recovery                                    │
│  ┌────────────────────────────────────────────┐                │
│  │ ConfigSyncReconciler triggers               │                │
│  │                                             │                │
│  │ 1. Fetch source: default/app-cfg           │                │
│  │    {"key": "value"}                         │                │
│  │                                             │                │
│  │ 2. Unconditional overwrite:                │                │
│  │    dev/app-cfg ← default/app-cfg           │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │ dev/app-cfg: {"key": "value"}  ✅ RESTORED │                │
│  │ Labels: synced-from, synced-by             │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘

Result: 
- Webhook: Immediate protection (preventive)
- Periodic Sync: Auto-recovery within 5 min (corrective)
- Dual protection ensures consistency
```

## Namespace 监控

```
┌─────────────────────────────────────────────────────────────────┐
│            Namespace Reconciler (Namespace Watcher)             │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Event: New namespace created                                  │
│  ┌────────────────────────────────────────────┐                │
│  │ kubectl create namespace production         │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │ NamespaceReconciler.Reconcile()            │                │
│  │                                             │                │
│  │ 1. Filter system namespaces:               │                │
│  │    - kube-system ❌                         │                │
│  │    - kube-public ❌                         │                │
│  │    - production ✅                          │                │
│  │                                             │                │
│  │ 2. Find matching ConfigSync:               │                │
│  │    - targetNamespaces contains "production" │                │
│  │    OR targetNamespaces is empty            │                │
│  │    - excludedNamespaces doesn't contain it  │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         │ Match found                                          │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │ Update ConfigSync annotation:               │                │
│  │   namespace-trigger: "2024-01-15T10:30:00Z" │                │
│  └────────────────────────────────────────────┘                │
│         │                                                       │
│         ▼                                                       │
│  ┌────────────────────────────────────────────┐                │
│  │ ConfigSyncReconciler triggered             │                │
│  │                                             │                │
│  │ Syncs resources to "production" namespace   │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 资源标签系统（双标签隔离）

```
┌─────────────────────────────────────────────────────────────────┐
│              Resource Label System (Dual Labels)                │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ConfigSync 1: "sync-a"                                         │
│  Source: namespace-a                                            │
│  ┌────────────────────────────────────────────┐                │
│  │ Synced resources get labels:               │                │
│  │   config.zyql.com/synced-from: namespace-a │             │
│  │   config.zyql.com/synced-by: sync-a     │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
│  ConfigSync 2: "sync-b"                                         │
│  Source: namespace-b                                            │
│  ┌────────────────────────────────────────────┐                │
│  │ Synced resources get labels:               │                │
│  │   config.zyql.com/synced-from: namespace-b │             │
│  │   config.zyql.com/synced-by: sync-b     │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
│  ✅ Benefits:                                                   │
│  1. Multiple ConfigSync can coexist safely                      │
│  2. Cleanup only affects owned resources                       │
│  3. Webhook protects based on both labels                      │
│  4. Easy to query resources by source or owner                 │
│                                                                 │
│  Example cleanup:                                               │
│  ┌────────────────────────────────────────────┐                │
│  │ When ConfigSync "sync-a" is deleted:       │                │
│  │                                             │                │
│  │ Delete all resources with BOTH:            │                │
│  │   synced-from: namespace-a                 │                │
│  │   synced-by: sync-a                        │                │
│  │                                             │                │
│  │ Resources from "sync-b" are NOT affected   │                │
│  └────────────────────────────────────────────┘                │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 事件流

```
User Action          ConfigSync           Resource           Webhook
    │                Controller           Watcher            Validator
    │                    │                    │                  │
    ├─ create ConfigSync ─────────────────────┐                  │
    │                    │                    │                  │
    │                    ▼                    │                  │
    │              Reconcile Start            │                  │
    │              - List sources             │                  │
    │              - Filter resources         │                  │
    │              - Sync to targets          │                  │
    │              - Update status            │                  │
    │                    │                    │                  │
    │ create source      │                    │                  │
    ├────────────────────┼────────────────────┼─────────────────▶│
    │  Secret/ConfigMap  │                    │                  │
    │                    │                    │                  │
    │                    │                    ▼                  │
    │                    │              Watch Event              │
    │                    │              Check selectors          │
    │                    │                    │                  │
    │                    │◀────trigger────────┤                  │
    │                    │              Add annotation           │
    │                    │                    │                  │
    │                    ▼                    │                  │
    │              Reconcile                  │                  │
    │              Quick sync                 │                  │
    │                    │                    │                  │
    │ modify synced      │                    │                  │
    ├────────────────────┼────────────────────┼─────────────────▶│
    │  resource          │                    │                  │
    │                    │                    │                  │
    │                    │                    │                  ▼
    │                    │                    │            Check labels
    │                    │                    │            Both exist?
    │                    │                    │                  │
    │◀─ Denied ──────────┼────────────────────┼──────────────────┤
    │  Error message     │                    │                  │
    │                    │                    │                  │
    │ delete ConfigSync  │                    │                  │
    ├────────────────────▶│                   │                  │
    │                    │                    │                  │
    │                    ▼                    │                  │
    │              Finalizer cleanup          │                  │
    │              - Delete synced resources  │                  │
    │              - Remove finalizer         │                  │
    │                    │                    │                  │
    │◀─── Completed ─────┘                    │                  │
    │                                         │                  │
    ▼                                         ▼                  ▼
```

## 部署架构（生产环境）

```
┌─────────────────────────────────────────────────────────────────┐
│                          ArgoCD                                 │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Application: config-sync-operator                       │  │
│  │  - Auto Sync: Enabled                                    │  │
│  │  - Prune: Enabled                                        │  │
│  │  - Self Heal: Enabled                                    │  │
│  └──────────────────────────────────────────────────────────┘  │
│                              │                                  │
│                              │ watches Git repo                │
│                              ▼                                  │
├─────────────────────────────────────────────────────────────────┤
│                      Git Repository                             │
│                                                                 │
│  deploy/                                                        │
│  ├── crd.yaml                                                   │
│  ├── rbac.yaml                                                  │
│  ├── deployment.yaml                                            │
│  ├── service.yaml                                               │
│  ├── cert-manager-certs.yaml  ← Automatic cert management      │
│  ├── webhook.yaml             ← cert-manager injects CA        │
│  └── examples.yaml                                              │
│                                                                 │
│                              │                                  │
│                              │ syncs to                         │
│                              ▼                                  │
├─────────────────────────────────────────────────────────────────┤
│                    Kubernetes Cluster                           │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  cert-manager (prerequisite)                             │  │
│  │  - Self-signed Issuer                                    │  │
│  │  - CA Certificate                                        │  │
│  │  - Webhook Certificate                                   │  │
│  │  - Auto renewal                                          │  │
│  └──────────────────────────────────────────────────────────┘  │
│                              │                                  │
│                              ▼                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  config-sync-operator                                    │  │
│  │  - Deployment (1 replica)                                │  │
│  │  - Service (webhook)                                     │  │
│  │  - ValidatingWebhookConfiguration                        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## 关键特性总结

### 1. **四控制器架构**
- ConfigSyncReconciler: 主同步逻辑 + 5分钟定期同步
- SecretReconciler: 监听 Secret 变化 → 即时同步
- ConfigMapReconciler: 监听 ConfigMap 变化 → 即时同步
- NamespaceReconciler: 监听新 namespace → 自动同步

### 2. **双层保护**
- **预防层**: Webhook 拦截非法修改
- **修复层**: 5分钟定期同步自动恢复

### 3. **双标签隔离**
- `synced-from`: 标识源 namespace
- `synced-by`: 标识 ConfigSync 名称
- 支持多个 ConfigSync 实例共存

### 4. **GitOps 友好**
- cert-manager 自动管理证书
- ArgoCD 自动部署和同步
- 声明式配置

### 5. **事件优化**
- Predicate 过滤减少无效调谐
- 时间戳注解确保唯一性
- 选择器匹配避免不必要的同步
