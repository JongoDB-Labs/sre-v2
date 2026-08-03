# Gitea Onboarding — Mission App #2

> **Status:** design (approved 2026-08-03). Executes HANDOFF §3 "Onboard mission app #2".
> **Sibling docs:** `docs/app-onboarding.md` (the recipe this exercises and will extend),
> `docs/specs/app-catalog-round2-design.md` (the catalog contract), `docs/platform-runbook.md`
> (gotcha catalog this will grow).
> **Implements into:** a new **`JongoDB-Labs/sre-apps`** repo (the app packaging) and
> **sre-v2** (catalog entry, resolver hardening, operator Package CRs, docs).

## 0. Goal & why Gitea

Onboard a second mission app — **off-the-shelf, not authored by us** — through the exact
`srectl app install` path cosmos defined, to surface and fix what in the substrate + DSOP is
cosmos-specific vs truly generic. Gitea was chosen (user decision 2026-08-03) because it
cleanly exercises every seam cosmos does **not**:

| Seam | cosmos (app #1) | Gitea (app #2) |
|---|---|---|
| SSO | hand-wired per-org OIDC RP; client created manually; secret sealed into app DB | **declarative `Package.sso`** plain client; secret shaped by `secretConfig.template`; zero glue code |
| Postgres | SUPERUSER role + app-owned SOPS password + migrate-hook role creation | least-priv owner role consuming **PGO's auto-generated pguser secret** |
| Object storage | bring-your-own MinIO StatefulSet + imperative `mc` init | **MinIO-operator `Tenant` CR** — first exercise of the substrate's declared model |
| Monitoring | no `Package.monitor` block | **`Package.monitor` ServiceMonitor** |
| Supply chain | bundle never actually published (`ghcr.io/jongodb-labs/bundles/cosmos` = NAME_UNKNOWN) | **first end-to-end publish → keyless sign → verify → catalog install**, under a **second signer identity** |

Decisions locked with the user 2026-08-03: app = **Gitea**; packaging home = **new
`JongoDB-Labs/sre-apps` public monorepo** (gitea first, future OSS apps as siblings); object
storage = **Tenant CR in the gitea namespace**; `requires` preflight fixed by giving the
**operator packages minimal Package CRs**.

## 1. Scope & non-goals

**In scope:** the Gitea UDS package + bundle + signed release pipeline in sre-apps; the
sre-v2 catalog entry + OCI-resolver hardening; minimal Package CRs for `packages/pgo` and
`packages/minio`; doc updates (second worked example, new gotchas); lab acceptance.

**Non-goals:** P6 update orchestration (design-gated — separate brainstorm; NEVER a direct
mutation); fixing cosmos-v2's missing bundle publish (follow-on: cosmos copies the sre-apps
workflow); Gitea HA (single replica by design, see §3.2); prod object storage (operator is
upstream-EOL — lab-only; S3 endpoint/creds abstracted for the external-S3 swap); Gitea SSH
git transport (HTTPS-only at first; SSH ingress via the gateway is a follow-on if wanted);
SP4 Flux, Loki/Velero.

## 2. Architecture — what lives where

```
JongoDB-Labs/sre-apps  (NEW, public, AGPL like sre-v2)
  gitea/
    chart/                 # thin wrapper chart "gitea-uds": Package CR, PostgresCluster,
                           #   MinIO Tenant, mc bootstrap Job, glue config
    zarf.yaml              # zarf package "gitea": upstream helm chart (pinned) + wrapper chart + images
    bundle/uds-bundle.yaml # UDSBundle "gitea" — contains ONLY the gitea zarf package
                           #   (apps deploy onto a RUNNING substrate; contrast cosmos-stack's
                           #   all-in-one airgap bundle)
  .github/workflows/release.yaml   # tag gitea-v* → create → publish → sign → verify → attest

JongoDB-Labs/sre-v2  (this repo)
  catalog.yaml                     # + gitea entry (second signer identity)
  installer/internal/appcatalog/   # OCI resolver hardening
  packages/pgo, packages/minio     # + minimal Package CRs (named "pgo"/"minio")
  docs/                            # onboarding worked example #2, runbook gotchas
```

**Naming contract (load-bearing):** the zarf package name, the bundle `metadata.name`, the
catalog entry `name`, and the app's `Package` CR name are all exactly **`gitea`** — the
catalog's rollback (`uds remove <name>`), drift, and status logic key on that single name
(appcatalog `deploy.go`, `app.go`).

## 3. The Gitea package (sre-apps)

### 3.1 Upstream chart + values

Upstream Gitea Helm chart **v12.7.0** (app **1.27.0**) from `https://dl.gitea.com/charts/`,
pinned; rootless image `docker.gitea.com/gitea:1.27.0-rootless` **digest-pinned** in
zarf.yaml (multi-arch amd64+arm64 verified upstream). Key values:

```yaml
# all four bundled subcharts OFF — postgresql-ha and valkey-cluster default ON
postgresql: {enabled: false}
postgresql-ha: {enabled: false}
valkey-cluster: {enabled: false}
valkey: {enabled: false}

replicaCount: 1                    # git repos are filesystem-only (no S3 backend exists);
strategy: {type: Recreate}         #   RWO PVC ⇒ 1 replica; Recreate avoids the RollingUpdate
persistence: {size: 10Gi}          #   maxSurge-100% volume-attach deadlock

gitea:
  admin:
    existingSecret: gitea-admin-creds   # SOPS-provisioned out-of-band (keys: username, password)
    passwordMode: initialOnlyNoReset    # default keepUpdated silently re-resets on upgrade
  config:
    server:
      DOMAIN: gitea.uds.dev
      ROOT_URL: https://gitea.uds.dev/  # chart ingress disabled; TLS terminates at the gateway
      SSH_DOMAIN: gitea.uds.dev
    session: {PROVIDER: memory}         # valkey disabled ⇒ explicit single-replica fallbacks
    cache: {ADAPTER: memory}
    queue: {TYPE: level}
    metrics: {ENABLED: true}
    service:
      ALLOW_ONLY_EXTERNAL_REGISTRATION: true   # NOT DISABLE_REGISTRATION: true — that blocks
      SHOW_REGISTRATION_BUTTON: false          #   OIDC auto-registration too (lockout trap)
    oauth2_client:
      ENABLE_AUTO_REGISTRATION: true
      ACCOUNT_LINKING: auto
      USERNAME: preferred_username
```

`ENABLE_PASSWORD_SIGNIN_FORM` stays **true** in the lab (admin break-glass); flipping it
false is the documented GOV/SSO-only hardening knob.

### 3.2 SSO seam — declarative `Package.sso`

Plain confidential client (Gitea is a native OIDC RP — **no** `enableAuthserviceSelector`),
declared in the wrapper chart's Package CR:

```yaml
sso:
  - name: "Gitea"
    clientId: gitea
    enabled: true
    standardFlowEnabled: true
    publicClient: false
    redirectUris:
      - "https://gitea.uds.dev/user/oauth2/keycloak/callback"   # /user/oauth2/<auth-name>/callback
    secretConfig:
      name: gitea-oidc-client
      template:                     # the gitea chart's oauth existingSecret expects EXACTLY
        key: "clientField(clientId)"    #   keys named "key" and "secret"
        secret: "clientField(secret)"
```

UDS creates the client in the `uds` realm and writes `gitea-oidc-client` **into the gitea
namespace**; the chart consumes it with:

```yaml
gitea:
  oauth:
    - name: keycloak
      provider: openidConnect
      existingSecret: gitea-oidc-client
      autoDiscoverUrl: https://sso.uds.dev/realms/uds/.well-known/openid-configuration
      scopes: "openid profile email"
```

Egress: Gitea resolves **all** OIDC endpoints from the public discovery document, so one
scoped rule suffices (no `Anywhere` — cosmos's caveat, done right):

```yaml
- description: "SSO — OIDC discovery/authorize/token via tenant gateway"
  direction: Egress
  selector: {app.kubernetes.io/name: gitea}
  remoteNamespace: istio-tenant-gateway
  remoteSelector: {app: tenant-ingressgateway}
  port: 443
```

### 3.3 Postgres seam — PGO-native

Wrapper chart carries a `PostgresCluster` `gitea-pg` (postgresVersion 16, 1×instance 5Gi RWO,
pgBackRest repo1 5Gi volume — same lab posture as cosmos):

```yaml
users:
  - name: gitea            # no underscore (PGO DNS-label rule); NO SUPERUSER (cosmos grants it)
    databases: [gitea]
```

Gitea consumes **PGO's auto-generated** `gitea-pg-pguser-gitea` secret directly — no SOPS
password, no migrate hook (the cosmos pattern is app-specific, not the generic recipe):

```yaml
gitea:
  config:
    database: {DB_TYPE: postgres, HOST: gitea-pg-primary:5432, NAME: gitea, USER: gitea,
               SSL_MODE: verify-full}
  additionalConfigFromEnvs:
    - name: GITEA__DATABASE__PASSWD
      valueFrom: {secretKeyRef: {name: gitea-pg-pguser-gitea, key: password}}
deployment:
  env:                                     # deployment.env reaches init + main containers —
    - name: PGSSLROOTCERT                  #   configure_gitea runs `gitea migrate` against PG
      value: /etc/pg-ca/ca.crt
extraVolumes:        [{name: pg-ca, secret: {secretName: gitea-pg-cluster-cert, items: [{key: ca.crt, path: ca.crt}]}}]
extraContainerVolumeMounts: [{name: pg-ca, mountPath: /etc/pg-ca, readOnly: true}]
extraInitVolumeMounts:      [{name: pg-ca, mountPath: /etc/pg-ca, readOnly: true}]
```

(`verify-full` against PGO's per-cluster CA: Gitea 1.27 uses lib/pq, which honors
`PGSSLROOTCERT`; there is no app.ini key for the root cert. Risk note §9.)

Package `allow` includes `{direction: Egress, remoteGenerated: KubeAPI}` — Patroni's DCS
(runbook gotcha #11), plus IntraNamespace both directions.

### 3.4 Object-storage seam — MinIO Tenant CR (first real exercise)

A `Tenant` (minio.min.io/v2) `gitea-minio` in the **gitea namespace**: 1 server / 1 volume
(lab), `requestAutoCert: false` (HTTP path-style in-namespace, matching the lab's cosmos
posture; substrate-wide MinIO TLS is a separate pending decision), inline
`buckets: [{name: gitea}]`, root creds from SOPS-provisioned `gitea-minio-creds`
(`spec.configuration.name`). The tenant server image comes from the substrate's
`packages/minio` (not bundled here).

Least-priv IAM stays imperative by design (packages/minio README: "the Tenant CR provisions
the bucket; the app owns the retention + IAM posture"): an idempotent **`mc` bootstrap Job**
(image `minio/mc`, digest-pinned, bundled) creates user + bucket-scoped policy from a second
SOPS secret `gitea-minio-app-creds`, which Gitea consumes:

```yaml
gitea:
  config:
    storage: {STORAGE_TYPE: minio, MINIO_ENDPOINT: "gitea-minio-hl:9000",
              MINIO_BUCKET: gitea, MINIO_USE_SSL: false, MINIO_BUCKET_LOOKUP: path}
  additionalConfigFromEnvs:
    - {name: GITEA__STORAGE__MINIO_ACCESS_KEY_ID,     valueFrom: {secretKeyRef: {name: gitea-minio-app-creds, key: accessKey}}}
    - {name: GITEA__STORAGE__MINIO_SECRET_ACCESS_KEY, valueFrom: {secretKeyRef: {name: gitea-minio-app-creds, key: secretKey}}}
```

The global `[storage]` section covers attachments, LFS, avatars, repo-archives, packages.
Git repositories themselves **cannot** live in object storage — the RWO PVC remains (§3.1).

**Expected new gotchas (the point of this exercise):** the operator (in `minio-operator` ns)
must reach the tenant pods in the now-default-deny gitea namespace ⇒ the Package needs an
**Ingress allow from `remoteNamespace: minio-operator`**, and tenant pods likely need KubeAPI
egress. Exact rules confirmed at lab acceptance and added to the runbook + onboarding doc.

### 3.5 Ingress + monitoring

```yaml
network:
  expose:
    - service: gitea-http
      selector: {app.kubernetes.io/name: gitea}   # confirm exact pod labels from helm template
      host: gitea
      gateway: tenant
      port: 3000
monitor:
  - selector: {app.kubernetes.io/name: gitea}
    targetPort: 3000
    portName: http
    path: /metrics
    kind: ServiceMonitor
```

The monitor entry auto-creates the Prometheus scrape netpol — no manual allow rule.

### 3.6 Zarf package + bundle

`gitea/zarf.yaml`: kind ZarfPackageConfig, name `gitea`, one required component with the
upstream chart (repo URL + version 12.7.0), the wrapper chart (localPath), and digest-pinned
images (`docker.gitea.com/gitea:1.27.0-rootless`, `minio/mc`). Namespace `gitea`. Secrets
(`gitea-admin-creds`, `gitea-minio-creds`, `gitea-minio-app-creds`) are **not** baked in —
SOPS out-of-band, same rule as cosmos.

`gitea/bundle/uds-bundle.yaml`: kind UDSBundle, name `gitea`, version = release version,
containing only the gitea zarf package. **Versioning:** repo tag `gitea-v<appver>-<rev>`
(e.g. `gitea-v1.27.0-1`) → bundle version `1.27.0-1`; rev bumps for packaging-only changes.

## 4. Release pipeline (sre-apps CI)

Mirrors cosmos-v2's hardened release, applied to a bundle for the first time. On tag
`gitea-v*`:

1. **Lint/render gate:** `helm template` the wrapper chart + `zarf dev lint`.
2. **Create:** `zarf package create` → `uds create` (amd64 first; arm64 added if the lab
   turns out arm64 — open question §9).
3. **Publish:** `uds publish` → `ghcr.io/jongodb-labs/bundles/gitea:<version>`.
4. **Sign (keyless, always):** `cosign sign --yes` on the **published OCI manifest digest**
   (GitHub OIDC → Fulcio/Rekor). Registry-level signature — this is what the catalog's
   fail-closed `CheckSignature` verifies; `uds deploy` itself does not check it (§9).
5. **Verify-after-sign gate:** up to 3 attempts of `cosign verify
   --certificate-identity-regexp "^https://github.com/JongoDB-Labs/sre-apps/"
   --certificate-oidc-issuer https://token.actions.githubusercontent.com` against the
   re-resolved published digest, re-signing on a miss; release **fails** if unverifiable.
   (Copied from cosmos-v2, which once shipped green-but-unsigned.)
6. **SLSA provenance:** `actions/attest-build-provenance` on the bundle digest,
   `push-to-registry: true`.

One-time manual step after first publish: flip the GHCR package
`jongodb-labs/bundles/gitea` to **public** (no API for visibility).

## 5. sre-v2 changes

### 5.1 Catalog entry #2 — second signer identity

```yaml
- name: gitea
  version: "1.27.0-1"
  description: "Gitea — self-hosted git forge (mission app #2, first OSS onboarding)."
  source: {type: oci, ref: ghcr.io/jongodb-labs/bundles/gitea}
  verify:
    identityRegexp: "^https://github.com/JongoDB-Labs/sre-apps/"
    issuer: "https://token.actions.githubusercontent.com"
  requires: [pgo, minio]
```

Verification is already per-entry (`verify.go`) — no code change to support the second
identity. `shipped_catalog_test.go` gains gitea-as-entry-#2 assertions (cosmos stays #1).

### 5.2 OCI resolver hardening

Current `source/oci.go` greps `zarf package inspect` output for the *first* `sha256:` and
ignores `entry.version` (resolves `:latest` implicitly); it has never run against a real
published UDSBundle. Change:

- Resolve the ref **tagged with `entry.version`** when the catalog ref carries no tag.
- Obtain the digest via **`zarf tools registry digest <ref>:<tag>`** (embedded crane —
  returns exactly the OCI manifest digest that cosign signed/verifies), replacing the
  first-sha256 grep.
- Keep the returned ref digest-pinned (`<ref>@<digest>`) for verify + deploy, unchanged.

TDD against the existing fake-exec harness; validated live against the published gitea
bundle at acceptance.

### 5.3 Operator Package CRs (`requires` truthfulness + hardening)

The preflight matches `requires` against live **UDS Package CR names**, but neither operator
package creates one — so the advisory `missing-require` warning always fires. Fix: minimal
`Package` CRs named **`pgo`** (in `postgres-operator` ns) and **`minio`** (in
`minio-operator` ns), carrying the allows the operators actually need (KubeAPI egress,
IntraNamespace; exact set determined on the lab). This makes `requires` truthful **and**
brings the operators under default-deny.

⚠️ This changes live-cluster network posture for running operators — **own PR slice,
lab-verified first** (destructive-test guardrail), with the netpol effect observed before/
after (`kubectl get netpol -n postgres-operator/minio-operator`, PG failover exercised).

### 5.4 Docs

- `docs/app-onboarding.md`: Gitea becomes the **second worked example** — specifically the
  declarative `Package.sso` + `secretConfig.template` path, the pguser-secret Postgres
  pattern, the Tenant-CR object-storage pattern, the scoped SSO egress rule, and the
  `monitor` seam (all the places where "cosmos does X" was previously the only story).
- `docs/platform-runbook.md`: new gotchas harvested at acceptance (operator↔tenant netpols
  expected, §3.4); note the two coexisting gotcha-numbering schemes or unify them.
- `catalog.yaml` + `installer/internal/catalog/catalog.yaml`: clear the `minio: status
  pending` drift (it ships in the bundle today).

## 6. Testing strategy

Per the handoff build style: subagent-driven TDD, PR-per-slice, squash-merge, per-task
spec+quality review + final whole-branch review.

- **srectl (S1, S4):** unit TDD with the existing fake-exec harness (no cluster).
- **Wrapper chart (S2):** `helm template` golden-file tests (Package CR, PostgresCluster,
  Tenant, Job render as specced; subcharts verifiably disabled) + `helm lint`.
- **CI (S3):** workflow validated by a dry-run path (`zarf dev lint`, create without
  publish) on PRs; the publish→sign→verify path proves itself on the first real tag.
- **Lab acceptance (S6):** the real bar — see §7. Destructive/mutating steps lab-only.

## 7. Acceptance (round-2 bar for gitea)

Preconditions: substrate Ready; pgo + minio operator packages deployed; tunnel restored.

1. `srectl app list` shows gitea (entry #2).
2. `srectl app install gitea` → `resolved gitea → …@sha256:…` → `signature verified`
   (sre-apps identity) → advisory warnings none (post-S5) → `deployed gitea` → `recorded`.
   Fail-closed check: tampered/unsigned ref must abort before `uds deploy`.
3. Cohesion wired, all four seams:
   - VirtualService for `gitea.uds.dev` exists; page serves via the tenant gateway.
   - Secret `gitea-oidc-client` exists in ns gitea with keys `key`/`secret`; Keycloak
     client `gitea` exists in the `uds` realm.
   - `gitea-pg` PostgresCluster healthy; Gitea connects `verify-full`.
   - Tenant `gitea-minio` green; bucket `gitea` exists.
   - ServiceMonitor exists; Prometheus target up.
4. Functional: log in via Keycloak test user (auto-registered); create repo; push over
   HTTPS; upload an attachment/LFS object → object visible in the bucket (`mc ls`).
5. `srectl app status gitea` green, `LIVE true`, no drift; install-record ConfigMap has the
   gitea key.
6. `srectl app remove gitea` cleans up; record pruned (teardown optional).

## 8. PR slices & sequencing

| # | Repo | Slice | Depends on |
|---|---|---|---|
| S1 | sre-v2 | OCI resolver hardening (`zarf tools registry digest`, version-tag resolution) + tests | — |
| S2 | sre-apps | Repo scaffold (AGPL, README) + wrapper chart + golden tests | — |
| S3 | sre-apps | zarf.yaml + bundle + release workflow (publish→sign→verify→attest); first tagged release | S2 |
| S4 | sre-v2 | catalog gitea entry + shipped-catalog test + catalog-drift doc fix | S3 (needs a real ref to point at; entry can land with the first real version) |
| S5 | sre-v2 | Operator Package CRs (pgo, minio) — **lab-gated** | tunnel restored |
| S6 | both | Lab acceptance run; gotcha harvesting; onboarding worked example #2 + runbook updates | S1–S5, tunnel |

## 9. Risks & open questions

- **Lab unreachable (2026-08-03):** cosmos-ssh cloudflared tunnel down (HTTP 530). S1–S4
  proceed; S5/S6 blocked until the user restores it.
- **Lab node arch unverified:** CI builds amd64 bundles first; check
  `kubectl get nodes -o wide` at first access and add an arm64 build if needed.
- **`PGSSLROOTCERT` is driver behavior, not chart API:** lib/pq honors it today; a future
  Gitea switch to pgx also honors it, but re-verify on Gitea major upgrades.
- **Signing-model split:** uds/zarf's internal key-based signatures (inside the artifact)
  vs cosign registry-level keyless signatures (beside it). The catalog's authority is the
  **cosign registry-level signature** (fail-closed `CheckSignature`); `uds deploy` does not
  verify it — document this explicitly in the onboarding doc so nobody assumes double
  coverage.
- **MinIO operator upstream-EOL** (archived 2026-03; 7.1.1 last AGPL): acceptable lab-only;
  Gitea's S3 wiring is endpoint/creds-abstracted so prod swaps to external S3 without chart
  surgery. The substrate-wide object-store decision remains open and is NOT resolved here.
- **Operator Package CRs may disrupt running operators** (netpol tightening): lab-gated
  slice with explicit before/after verification and rollback path (delete the Package CR).
- **Chart v13 will remove the `valkey-cluster` subchart:** values referencing
  `valkey-cluster.enabled` need adjusting at that upgrade; pinned 12.7.0 unaffected.
- **GHCR visibility:** one-time manual public toggle after first publish (no API).

## 10. Guardrails (restated from HANDOFF §5)

P6 update orchestration stays design-gated and out of scope — nothing here mutates
installed versions outside the catalog's install path. Destructive/mutating tests lab-only
(`cosmos-k8s`), never prod. Honesty rule: lab = `cosmos.uds.dev`; prod is still Compose.
