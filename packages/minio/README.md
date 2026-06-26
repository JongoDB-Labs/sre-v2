# MinIO Operator — SRE object-store data-service

The substrate ships the **MinIO Operator** cluster-wide (namespace `minio-operator`).
Each mission app provisions its **own isolated object store** by declaring a `Tenant`
CR in its namespace — the operator reconciles it into an S3-compatible MinIO with the
app's buckets and credentials. Shared operator, isolated per-app data — the same model
as [`pgo/`](../pgo/).

For **prod/DoD** an external FIPS S3 replaces MinIO (per the install posture), so this
is the **lab/baseline** object store only.

| | |
|---|---|
| Operator chart | `operator` 7.1.1 from `https://operator.min.io` (classic Helm repo) |
| Operator image | `quay.io/minio/operator:v7.1.1` (also the Tenant init+sidecar) |
| Tenant server image | `quay.io/minio/minio:RELEASE.2025-04-08T15-41-24Z` |
| Tenant CRD | `minio.min.io/v2`, kind `Tenant` |

## How an app declares a Tenant

The operator is cluster-wide, so the app just includes a `Tenant` in its chart. The
substrate already carries the `minio` server image, so the app's own package doesn't
need it. Buckets — including the **object-locked/WORM** bucket — are declared inline;
the operator creates them at provisioning time. Root creds come from a secret named by
`spec.configuration.name` (manage it with SOPS, like the app's other secrets).

```yaml
apiVersion: minio.min.io/v2
kind: Tenant
metadata:
  name: cosmos
  namespace: cosmos
spec:
  image: quay.io/minio/minio:RELEASE.2025-04-08T15-41-24Z   # carried by the substrate
  configuration:
    name: cosmos-minio-creds          # SOPS-managed; holds CONFIG_ENV with MINIO_ROOT_USER/PASSWORD
  pools:
    - name: pool-0
      servers: 1                      # lab single-node; bump for distributed/erasure-coded
      volumesPerServer: 1
      volumeClaimTemplate:
        spec:
          accessModes: [ReadWriteOnce]
          resources: { requests: { storage: 10Gi } }
  buckets:
    - name: cosmos-uploads
    - name: cosmos-pgbackrest
    - name: cosmos-audit-worm
      objectLock: true                # WORM — must be set at bucket creation
```

Notes:
- `objectLock: true` only **enables** locking on the bucket (it must be set at creation,
  not retrofitted). The **default COMPLIANCE retention** and least-privilege
  service-account policies are still applied by the app's own init step (`mc retention
  set --default COMPLIANCE …`, policy/user create) against the Tenant endpoint — the
  Tenant CR provisions the bucket; the app owns the retention + IAM posture.
- The Tenant exposes the S3 API on the in-namespace service (`https://<tenant>-hl` /
  the `minio` service the operator creates) on port `9000`; the app points
  `S3_ENDPOINT` at it.
- The operator drives Tenants through the kube API — an app namespace under UDS
  default-deny needs `KubeAPI` egress allowed for the operator to manage the Tenant
  there (same requirement PGO has; see the bundle/runbook).
