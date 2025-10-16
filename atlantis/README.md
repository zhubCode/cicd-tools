# Atlantis Deployment Prerequisites

This README describes the prerequisites for deploying Atlantis using ArgoCD. Before ArgoCD can successfully sync this configuration, you must manually create the required TLS certificate secret.

## Prerequisites

### 1. TLS Certificate Secret

The Atlantis ingress configuration references a TLS secret named `zyql.com` that must be created before ArgoCD synchronization.

**Location**: The secret must be created in the `atlantis` namespace.

**Secret Reference**: As seen in `atlantis.yaml` at line 471:
```yaml
tls:
- hosts:
  - atlantis.zyql.com
  secretName: zyql.com
```

### 2. Certificate Installation Steps

#### Step 1: Download Certificate from Volcengine
1. Navigate to the Volcengine Certificate Center: https://console.volcengine.com/certificate-center/ssl/certificate/cert-ffc9b54815644daa81f03d4dcb8bac52
2. Download the certificate bundle for your domain
3. Extract the certificate files (typically `_.zyql.com.pem` and `_.zyql.com.key`)

#### Step 2: Create Kubernetes Secret
Before running ArgoCD sync, execute the following command to create the TLS secret:

```bash
kubectl -n atlantis create secret tls zyql.com \
  --cert=_.zyql.com_nginx/_.zyql.com.pem \
  --key=_.zyql.com_nginx/_.zyql.com.key
```

**Important Notes:**
- Ensure the `atlantis` namespace exists before creating the secret
- The certificate files path should match your local directory structure
- The secret name `zyql.com` must match exactly what's referenced in the ingress configuration

### 3. Verification

After creating the secret, verify it exists:

```bash
kubectl -n atlantis get secret zyql.com -o yaml
```

## ArgoCD Sync Order

1. **First**: Create the TLS certificate secret manually (as described above)
2. **Then**: ArgoCD can sync the Atlantis configuration successfully

Without the pre-existing TLS secret, the ArgoCD sync will fail because the ingress controller cannot find the referenced certificate.