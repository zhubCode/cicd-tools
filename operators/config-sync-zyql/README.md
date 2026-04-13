# Config Sync Operator

一个用于同步 Kubernetes Secret 和 ConfigMap 到指定 namespace 的 Operator。

## 📋 目录

- [功能特性](#功能特性)
- [架构说明](#架构说明)
- [安装部署](#安装部署)
- [验证安装](#验证安装)
- [使用示例](#使用示例)
- [配置说明](#配置说明)
- [故障排查](#故障排查)
- [卸载](#卸载)

## ✨ 功能特性

- ✅ 从源 namespace 同步 Secret 和 ConfigMap 到目标 namespaces
- ✅ 支持选择特定的目标 namespaces 或同步到所有 namespaces
- ✅ 支持排除特定的 namespaces
- ✅ 支持通过 Label Selector 过滤要同步的资源
- ✅ 可配置同步间隔（默认5分钟，最小60秒）
- ✅ 自动监听源资源的变化并实时同步
- ✅ 源资源删除时自动级联删除所有同步副本
- ✅ Webhook 保护：防止手动修改或删除已同步的资源
- ✅ 防止同步循环（已同步的资源不会被再次同步）
- ✅ 提供详细的同步状态和统计信息

## 🏗️ 架构说明

该 Operator 包含四个控制器和两个 Webhook 验证器：

**控制器**：
1. **ConfigSyncReconciler**: 主控制器，负责协调同步配置并执行同步操作
2. **SecretReconciler**: 监听 Secret 变化，触发相关的 ConfigSync 重新同步
3. **ConfigMapReconciler**: 监听 ConfigMap 变化，触发相关的 ConfigSync 重新同步
4. **NamespaceReconciler**: 监听新 namespace 创建，自动同步资源到新 namespace

**Webhook 验证器**：
1. **SecretValidator**: 验证 Secret 的更新和删除操作，保护已同步的资源
2. **ConfigMapValidator**: 验证 ConfigMap 的更新和删除操作，保护已同步的资源

详细架构文档请参考：[ARCHITECTURE.md](./ARCHITECTURE.md)

---

## 🚀 安装部署

### 前置条件

- Kubernetes 集群 v1.24+
- kubectl 命令行工具
- [cert-manager](https://cert-manager.io/) v1.13.0+ (用于 webhook 证书管理)

### 方法一：使用 Kustomize（推荐）

#### 1. 安装 cert-manager

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml

# 等待 cert-manager 准备就绪
kubectl wait --for=condition=Available --timeout=300s deployment/cert-manager -n cert-manager
kubectl wait --for=condition=Available --timeout=300s deployment/cert-manager-webhook -n cert-manager
kubectl wait --for=condition=Available --timeout=300s deployment/cert-manager-cainjector -n cert-manager
```

#### 2. 使用 Kustomize 部署

```bash
# 克隆仓库
git clone https://github.com/stepfun/config-sync-operator.git
cd config-sync-operator

# 使用 kustomize 部署（test 环境）
kubectl apply -k deploy/config-sync-operator/test
```

### 方法二：使用 ArgoCD

#### 1. 安装 cert-manager（如果未安装）

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
kubectl wait --for=condition=Available --timeout=300s deployment/cert-manager -n cert-manager
```

#### 2. 创建 ArgoCD Application（使用 Kustomize）

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: config-sync-operator
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/stepfun/config-sync-operator.git  # 修改为你的仓库地址
    targetRevision: main
    path: deploy/config-sync-operator/test  # 或使用 base
    kustomize:
      version: v5.0.0
  destination:
    server: https://kubernetes.default.svc
    namespace: config-sync-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

应用配置：

```bash
kubectl apply -f argocd-application.yaml

# 同步应用
argocd app sync config-sync-operator
```

---

## ✅ 验证安装

### 1. 检查 Namespace 和 CRD

```bash
# 检查 namespace 是否创建
kubectl get namespace config-sync-system

# 检查 CRD 是否安装
kubectl get crd configsyncs.config.stepfun.com
```

预期输出：
```
NAME                                 CREATED AT
configsyncs.config.stepfun.com       2025-11-21T08:00:00Z
```

### 2. 检查 Operator Pod 状态

```bash
# 查看 operator pod
kubectl get pods -n config-sync-system

# 查看 pod 详细信息
kubectl describe pod -n config-sync-system -l app=config-sync-operator
```

预期输出：
```
NAME                                    READY   STATUS    RESTARTS   AGE
config-sync-operator-xxxxxxxxxx-xxxxx   1/1     Running   0          2m
```

### 3. 检查 Webhook 配置

```bash
# 检查 ValidatingWebhookConfiguration
kubectl get validatingwebhookconfiguration config-sync-validating-webhook

# 检查 webhook service
kubectl get svc -n config-sync-system config-sync-webhook-service

# 检查证书是否注入（caBundle 不应为空）
kubectl get validatingwebhookconfiguration config-sync-validating-webhook -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d | openssl x509 -text -noout | head -n 5
```

### 4. 检查日志

```bash
# 查看 operator 日志
kubectl logs -n config-sync-system -l app=config-sync-operator -f

# 如果需要 debug 级别的日志，编辑 deployment
kubectl set env deployment/config-sync-operator -n config-sync-system ZAP_LOG_LEVEL=debug
```

预期日志（正常启动）：
```
{"level":"info","ts":"...","msg":"starting manager"}
{"level":"info","ts":"...","msg":"Starting EventSource","controller":"configsync"}
{"level":"info","ts":"...","msg":"Starting Controller","controller":"configsync"}
{"level":"info","ts":"...","msg":"Starting workers","controller":"configsync","worker count":1}
```

### 5. 快速功能测试

```bash
# 创建测试 namespace
kubectl create namespace test-source
kubectl create namespace test-target

# 创建测试 secret
kubectl create secret generic test-secret \
  --from-literal=username=admin \
  --from-literal=password=secret123 \
  -n test-source

# 创建 ConfigSync 资源
kubectl apply -f - <<EOF
apiVersion: config.stepfun.com/v1
kind: ConfigSync
metadata:
  name: test-sync
spec:
  sourceNamespace: test-source
  targetNamespaces:
    - test-target
  syncSecrets: true
  syncConfigMaps: false
EOF

# 等待几秒后检查同步结果
sleep 5
kubectl get secret test-secret -n test-target

# 验证 webhook 保护（应该被拒绝）
kubectl edit secret test-secret -n test-target
# 预期：Error from server: admission webhook "vsecret.kb.io" denied the request
```

如果所有检查都通过，说明安装成功！

---

## 📚 使用示例

### 示例 1: 同步 Docker Registry Secret 到所有命名空间

创建源 secret：
```bash
kubectl create secret docker-registry docker-registry \
  --docker-server=registry.example.com \
  --docker-username=user \
  --docker-password=password \
  --docker-email=user@example.com \
  -n config-sync-system
```

创建 ConfigSync：
```yaml
apiVersion: config.stepfun.com/v1
kind: ConfigSync
metadata:
  name: sync-docker-registry
spec:
  sourceNamespace: config-sync-system
  excludedNamespaces:
    - kube-system
    - kube-public
    - kube-node-lease
  syncSecrets: true
  syncConfigMaps: false
  secretSelector:
    matchLabels:
      type: kubernetes.io/dockerconfigjson
  syncIntervalSeconds: 300  # 每5分钟同步一次
```

### 示例 2: 同步应用配置到指定环境

```yaml
apiVersion: config.stepfun.com/v1
kind: ConfigSync
metadata:
  name: sync-app-config
spec:
  sourceNamespace: app-config
  targetNamespaces:
    - production
    - staging
    - development
  syncSecrets: true
  syncConfigMaps: true
  secretSelector:
    matchLabels:
      app: myapp
  configMapSelector:
    matchLabels:
      app: myapp
  syncIntervalSeconds: 120  # 每2分钟同步一次
```

### 示例 3: 同步 TLS 证书到特定命名空间

```yaml
apiVersion: config.stepfun.com/v1
kind: ConfigSync
metadata:
  name: sync-tls-certs
spec:
  sourceNamespace: cert-manager
  targetNamespaces:
    - frontend
    - backend
    - api
  syncSecrets: true
  syncConfigMaps: false
  secretSelector:
    matchLabels:
      cert-manager.io/certificate-name: wildcard-cert
  syncIntervalSeconds: 60  # 每分钟同步（证书更新需要快速同步）
```

### 示例 4: 同步到所有命名空间（除排除列表）

```yaml
apiVersion: config.stepfun.com/v1
kind: ConfigSync
metadata:
  name: sync-to-all
spec:
  sourceNamespace: shared-config
  # 不指定 targetNamespaces 表示同步到所有命名空间
  excludedNamespaces:
    - kube-system
    - kube-public
    - kube-node-lease
    - shared-config  # 排除源命名空间
  syncSecrets: true
  syncConfigMaps: true
  syncIntervalSeconds: 300
```

### 查看同步状态

```bash
# 查看所有 ConfigSync 资源
kubectl get configsyncs

# 查看详细状态
kubectl describe configsync sync-docker-registry

# 查看同步的 Secret（所有命名空间）
kubectl get secrets -A -l config.stepfun.com/synced-from

# 查看同步的 ConfigMap（所有命名空间）
kubectl get configmaps -A -l config.stepfun.com/synced-from

# 查看特定命名空间的同步资源
kubectl get secrets -n frontend -l config.stepfun.com/synced-from
kubectl get configmaps -n backend -l config.stepfun.com/synced-from

# 查看同步状态（JSON 格式）
kubectl get configsync sync-docker-registry -o json | jq '.status'
```

### 测试 Webhook 保护

```bash
# 尝试修改已同步的 secret（应该被拒绝）
kubectl edit secret docker-registry -n frontend

# 预期错误：
# Error from server: admission webhook "vsecret.kb.io" denied the request: 
# Secret 'docker-registry' is managed by ConfigSync (synced-from: config-sync-system, 
# synced-by: sync-docker-registry) and cannot be modified or deleted manually. 
# Please modify the source Secret in namespace 'config-sync-system' instead.

# 尝试删除已同步的 secret（应该被拒绝）
kubectl delete secret docker-registry -n frontend
# 同样会被拒绝
```

### 更新同步的资源

要更新已同步的资源，**只需修改源命名空间中的资源**：

```bash
# 修改源 secret
kubectl edit secret docker-registry -n config-sync-system

# 或使用 kubectl patch
kubectl patch secret docker-registry -n config-sync-system \
  --type='json' \
  -p='[{"op": "replace", "path": "/data/username", "value": "bmV3dXNlcg=="}]'

# Operator 会自动将变更同步到所有目标命名空间
```

### 删除同步的资源

删除源资源时，所有同步的副本会被**自动级联删除**：

```bash
# 删除源 secret
kubectl delete secret test-secret -n test-source

# 所有目标命名空间中的同步副本会被自动删除
kubectl get secret test-secret -n test-target
# Error from server (NotFound): secrets "test-secret" not found
```

---

## ⚙️ 配置说明

### ConfigSync 资源字段

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `sourceNamespace` | string | 否 | operator 所在 namespace | 源 namespace |
| `targetNamespaces` | []string | 否 | 所有 namespace | 目标 namespaces 列表 |
| `excludedNamespaces` | []string | 否 | `[]` | 要排除的 namespaces 列表 |
| `syncSecrets` | bool | 否 | `true` | 是否同步 Secrets |
| `syncConfigMaps` | bool | 否 | `true` | 是否同步 ConfigMaps |
| `secretSelector` | LabelSelector | 否 | 全部 | Secret 的标签选择器 |
| `configMapSelector` | LabelSelector | 否 | 全部 | ConfigMap 的标签选择器 |
| `syncIntervalSeconds` | int | 否 | `300` | 同步间隔（秒），最小 60 |

### 自动排除的 Namespaces

以下 namespace 会自动被排除，无需手动配置：
- `kube-system`
- `kube-public`
- `kube-node-lease`
- 源 namespace（避免自我复制）

### 自动排除的 Namespaces

以下 namespace 会自动被排除，无需手动配置：
- `kube-system` - Kubernetes 系统组件
- `kube-public` - 公共资源
- `kube-node-lease` - 节点租约
- 源 namespace（避免自我复制）
- `config-sync-system` - Operator 自身命名空间

### 同步标记

所有被同步的资源会自动添加以下标签：

```yaml
labels:
  config.stepfun.com/synced-from: <source-namespace>
  config.stepfun.com/synced-by: <configsync-name>
```

这些标记用于：
1. 标识资源来源
2. 防止同步循环
3. Webhook 保护识别
4. 便于查询和管理

### Operator 参数

Operator 支持以下启动参数：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--metrics-bind-address` | `:8080` | Metrics 端点地址 |
| `--health-probe-bind-address` | `:8081` | 健康检查端点地址 |
| `--enable-webhook` | `true` | 是否启用 webhook |
| `--webhook-port` | `9443` | Webhook 服务端口 |
| `--cert-dir` | `/tmp/k8s-webhook-server/serving-certs` | 证书目录 |
| `--zap-log-level` | `info` | 日志级别（info/debug/error）|
| `--enable-deployment` | `false` | 生产模式标志 |

启用 Debug 日志：
```bash
kubectl set env deployment/config-sync-operator -n config-sync-system \
  --containers=manager \
  -- \
  --zap-log-level=debug
```

---

## 🔧 故障排查

### 问题 1: Pod 无法启动

**症状**：Pod 处于 `CrashLoopBackOff` 或 `Error` 状态

**解决方案**：
```bash
# 查看 pod 状态
kubectl get pods -n config-sync-system

# 查看 pod 日志
kubectl logs -n config-sync-system -l app=config-sync-operator --previous

# 查看 pod 事件
kubectl describe pod -n config-sync-system -l app=config-sync-operator

# 常见原因：
# 1. CRD 未安装 - 重新应用 CRD
# 2. RBAC 权限不足 - 检查 ServiceAccount 和 ClusterRole
# 3. 证书未生成 - 检查 cert-manager 是否正常运行
```

### 问题 2: 资源未同步

**症状**：ConfigSync 已创建，但资源未出现在目标命名空间

**解决方案**：
```bash
# 1. 检查 ConfigSync 状态
kubectl describe configsync <name>

# 2. 查看 operator 日志
kubectl logs -n config-sync-system -l app=config-sync-operator -f

# 3. 检查源资源是否存在
kubectl get secrets -n <source-namespace>
kubectl get configmaps -n <source-namespace>

# 4. 检查 Label Selector 是否匹配
kubectl get secrets -n <source-namespace> --show-labels

# 5. 验证目标命名空间是否存在且未被排除
kubectl get namespaces

# 6. 启用 debug 日志查看详细信息
kubectl set env deployment/config-sync-operator -n config-sync-system -- --zap-log-level=debug
```

### 问题 3: Webhook 报错

**症状**：无法修改或删除资源，但提示 EOF 或连接错误

**解决方案**：
```bash
# 1. 检查 webhook service
kubectl get svc -n config-sync-system config-sync-webhook-service

# 2. 检查证书是否注入
kubectl get validatingwebhookconfiguration config-sync-validating-webhook \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d | openssl x509 -text -noout

# 3. 检查 cert-manager 是否正常
kubectl get pods -n cert-manager
kubectl get certificate -n config-sync-system

# 4. 重启 operator（重新加载证书）
kubectl rollout restart deployment config-sync-operator -n config-sync-system

# 5. 手动触发证书重新生成
kubectl delete certificate config-sync-webhook-cert -n config-sync-system
# cert-manager 会自动重新创建
```

### 问题 4: 资源删除后同步副本未删除

**症状**：删除源资源后，目标命名空间中的副本仍然存在

**解决方案**：
```bash
# 1. 检查 finalizer 是否存在
kubectl get configsync <name> -o yaml | grep finalizers

# 2. 检查 operator 日志是否有错误
kubectl logs -n config-sync-system -l app=config-sync-operator | grep -i delete

# 3. 手动清理（如果自动清理失败）
kubectl delete secrets -A -l config.stepfun.com/synced-by=<configsync-name>
kubectl delete configmaps -A -l config.stepfun.com/synced-by=<configsync-name>
```

### 查看日志

```bash
# 实时查看日志
kubectl logs -n config-sync-system -l app=config-sync-operator -f

# 查看最近 100 行日志
kubectl logs -n config-sync-system -l app=config-sync-operator --tail=100

# 查看前一个容器的日志（如果 pod 重启了）
kubectl logs -n config-sync-system -l app=config-sync-operator --previous

# 过滤错误日志
kubectl logs -n config-sync-system -l app=config-sync-operator | grep -i error
```

---

## ️ 卸载

### 完全卸载

```bash
# 1. 删除所有 ConfigSync 资源
kubectl delete configsyncs --all

# 2. 等待资源清理完成（finalizer 会清理所有同步的资源）
kubectl get configsyncs -w

# 3. 使用 kustomize 卸载
kubectl delete -k deploy/config-sync-operator/test

# 或直接删除 namespace
kubectl delete namespace config-sync-system

# 4. 删除 CRD
kubectl delete crd configsyncs.config.stepfun.com

# 5. 删除 ValidatingWebhookConfiguration
kubectl delete validatingwebhookconfiguration config-sync-validating-webhook
```

### 保留数据卸载

如果只想卸载 operator，但保留已同步的资源：

```bash
# 1. 删除所有 ConfigSync 的 finalizer
kubectl get configsyncs -o name | xargs -I {} kubectl patch {} --type=json -p='[{"op": "remove", "path": "/metadata/finalizers"}]'

# 2. 删除 ConfigSync 资源
kubectl delete configsyncs --all

# 3. 卸载 operator
kubectl delete -k deploy/config-sync-operator/test

# 已同步的 Secret 和 ConfigMap 会被保留，但不再受 webhook 保护
```

---

## 📝 许可证

MIT License

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！
