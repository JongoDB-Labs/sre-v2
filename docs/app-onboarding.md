# Onboarding a mission app onto SRE

How any business app plugs into the substrate's shared services. **cosmos is the
worked example**; the same pattern applies to any app.

An app is a **signed UDS Package** (a Helm chart + a `Package` CR) deployed onto a
*running* SRE (round 2, via the app-catalog). The `Package` CR is the single seam —
it wires the app into **ingress, SSO, observability, and network policy**. The app
brings its own **data instances** against the shared operators. Nothing about the
app is baked into the substrate.

---

## 1 · Ingress — `Package.expose`

```yaml
spec:
  network:
    expose:
      - service: <app-svc>
        selector: { app.kubernetes.io/name: <app>, app.kubernetes.io/component: web }
        host: <app>           # → https://<app>.uds.dev on the shared tenant gateway
        gateway: tenant
        port: 3000
```
UDS reconciles this into an Istio VirtualService on the **shared gateway** — every
app rides the same ingress + TLS. (cosmos: `charts/cosmos/templates/uds-package.yaml`.)

## 2 · Shared SSO — the substrate's one Keycloak, every app

The substrate runs **one Keycloak** (`core-identity-authorization`); every app
authenticates against it, so users get cohesive SSO across the whole ecosystem.

**Two halves:**

**(a) Provision a Keycloak client.** Either declare it in the app's `Package.sso`
(UDS creates the client + a k8s secret with id/secret), or create it by hand in the
realm. The client is **confidential**, with the app's callback as a redirect URI.

**(b) Point the app at it.** This is app-specific. cosmos is a **per-org OIDC
Relying Party** — it stores one `IdpConnection` row per org (no Authservice; cosmos
runs the full PKCE flow itself):

| Field | Value for a UDS Keycloak |
|---|---|
| `issuerUrl` | `https://sso.uds.dev/realms/uds` (the realm's discovery base) |
| `clientId` | the Keycloak client id (e.g. `cosmos-acme`) |
| `clientSecretEnc` | the client secret, **vault-sealed** (`sealSecret()`, key `SSO_VAULT_KEY`) |
| `scopes` | `["openid","email","profile"]` |
| `enabled` | `true` |
| redirect URI (register in Keycloak) | `https://<app-origin>/api/auth/sso/<org-slug>/callback` |

Then **Settings → Security** (or the API) can set `ssoEnforced: true` (GOV: SSO-only).
Login flow: `/login` → "Sign in with Keycloak" → `…/sso/<slug>/login` (discovery +
PKCE) → Keycloak → `…/sso/<slug>/callback` (token exchange, identity matched on
`(idpConnId, subject)` — never email) → session. Identity-investigation details +
the exact seed recipe live in cosmos-v2 `src/lib/auth/sso.ts` + `docs/sso-acceptance/`.

> **Cohesion takeaway:** the *substrate* owns the IdP; each app only registers a
> client + its own OIDC config. Swap the realm once → every app's SSO follows.

**Two real caveats (from wiring cosmos live):**
- **Egress.** A *native* OIDC app reaches Keycloak directly for discovery + token —
  but UDS does **not** auto-add that egress when it creates the SSO client. Add an
  egress `allow` to the app's `Package` (lab demo used `remoteGenerated: Anywhere`;
  tighten to the tenant gateway). Without it, the call to `sso.uds.dev` resolves
  (Istio ambient synthetic IP) but is **RST'd by ztunnel** under default-deny.
- **Vault key.** cosmos seals the IdP client secret with AES-256, so the app's
  `SSO_VAULT_KEY` must decode to **exactly 32 bytes** — generate with
  `openssl rand -base64 32` (a 48-byte key fails only when SSO is first used).

**Bootstrapping a login user (testing).** UDS Core ships **no standing Keycloak
admin** by design — you bootstrap one on demand and remove it after, so no permanent
superuser sits in the cluster ([UDS docs](https://docs.defenseunicorns.com/core/how-to-guides/identity--authorization/manage-admin-access/)).
Minting admin creds is **gated from automation** — it's an operator action (or a
sanctioned `uds zarf connect keycloak` → Welcome Page). To seed a test user:
```bash
KCPOD=$(kubectl -n keycloak get pod -l app.kubernetes.io/name=keycloak -o name | head -1)
TMPPW=$(openssl rand -base64 18)
kubectl -n keycloak exec "$KCPOD" -- env KCBOOT_PW="$TMPPW" bash -lc \
  '/opt/keycloak/bin/kc.sh bootstrap-admin user --username tmpbootstrap --password:env KCBOOT_PW'
kubectl -n keycloak exec "$KCPOD" -- env KCBOOT_PW="$TMPPW" bash -lc '
  K=/opt/keycloak/bin/kcadm.sh
  $K config credentials --server http://localhost:8080 --realm master --user tmpbootstrap --password "$KCBOOT_PW"
  $K create users -r uds -s username=ssotest -s email=ssotest@acme.test -s emailVerified=true -s enabled=true
  $K set-password -r uds --username ssotest --new-password "<pw>"
  TID=$($K get users -r master -q username=tmpbootstrap --fields id | grep -oE "[a-f0-9-]{36}" | head -1)
  [ -n "$TID" ] && $K delete users/$TID -r master   # remove the temp admin'
```
> ⚠️ **This recipe is known-broken against a live, already-running Keycloak pod** (found
> during Gitea acceptance, 2026-08-04 — `.superpowers/sdd/gitea-onboarding-plan/task-12a-report.md`
> §2.6). `kc.sh bootstrap-admin`, run via `kubectl exec` into a pod that's already serving,
> unconditionally triggers a full Quarkus re-augmentation build **inside the live
> container's cgroup** ("Changes detected in configuration. Updating the server image."),
> which OOM-kills the pod under the default `1Gi` memory limit — reproduced twice,
> deterministic (`lastState.terminated.reason: Error, exitCode: 137`). Even with memory
> headroom and the pod's actual `--features=preview,fips --fips-mode=strict` flags added
> to the command (both required, neither in the recipe above), it still fails, because
> **this Keycloak instance persists to an embedded, single-writer H2 database**
> (`/opt/keycloak/data/h2/keycloakdb.mv.db`; confirmed via `start-dev` boot args, no
> `KC_DB` env set anywhere, a 512Mi `keycloak-data` PVC — far too small for a real
> Postgres-backed deployment) rather than Postgres. `kc.sh bootstrap-admin` needs its
> **own** JDBC connection to create the temp admin, and a second connection can never
> coexist with the live server's exclusive lock on that file:
> `Database may be already in use ... The file is locked`. Neither more memory nor the
> right FIPS flags fix this — it is structural. The only ways around it are (a) scale the
> Keycloak StatefulSet to `0` and bootstrap standalone against the same PVC (an SSO
> outage for **every** app on the substrate, cosmos included, for the duration), or
> (b) migrate Keycloak to an external Postgres database (PGO already provides one on this
> substrate, per §3 below) so a second connection becomes possible — **neither is done
> today**, and neither was attempted live (both are substrate-config changes beyond a
> single onboarding run's remit).
>
> **This toil is exactly what the installer/Day-2 wizard must absorb** (`srectl` seeds
> the platform admin + users/service-creds) — and until Keycloak here is Postgres-backed
> or a sanctioned scale-to-zero bootstrap procedure is written and tested, **test-user
> seeding is an open substrate gap**, not a working recipe; Gitea's own acceptance run
> deferred its SSO-interactive-login check for exactly this reason. Also confirm the
> realm permits password login — the `uds` realm carries a `DENY_USERNAME_PASSWORD`
> hardening flag.

## 3 · Data — shared operators, own instances

The app declares its **own** isolated data against the substrate's shared operators
([`packages/`](../packages/README.md)):

- **Postgres:** a `PostgresCluster` CR (PGO is cluster-wide) → an isolated DB +
  pgBackRest. The substrate carries the postgres images; the app doesn't bundle them.
  Connect over the PGO-issued TLS (`sslmode=require&sslrootcert=…`) and **allow
  `KubeAPI` egress** in the Package (PGO/Patroni's DCS — runbook gotcha #11).
- **Object store:** per-app buckets (MinIO decision pending; prod = external S3).

## 4 · Network — default-deny + allow-list

The `Package` makes the namespace default-deny; declare what the app needs:
```yaml
    allow:
      - { direction: Egress,  remoteGenerated: IntraNamespace }   # app ↔ its DB/MinIO
      - { direction: Ingress, remoteGenerated: IntraNamespace }
      - { direction: Egress,  remoteGenerated: KubeAPI }          # if it runs PGO/Patroni
```

## 5 · Deploy

Round 2, onto a running SRE — the app-catalog (TUI + the SP8 web console, shared
backend) pulls the **signed** app package from local / OCI / GitHub and
`uds`/`zarf` deploys it; the `Package` CR auto-wires §1–§4. First org/admin is minted
by `bootstrap-org` (cosmos-v2 `prisma/seed/bootstrap-org.ts`). cosmos is the
reference implementation of every section above.

## Round-2 acceptance — deploy cosmos via the catalog

This is the round-2 "MVP done" bar (app-catalog spec §11): cosmos, re-deployed
**through `srectl app`**, wiring substrate cohesion automatically. It runs against
the live SRE (RKE2 + UDS Core) with a reachable bundle registry and the `uds`,
`zarf`, `cosign`, `kubectl` binaries on PATH. It is a manual/integration check —
the unit suite (`go test ./...`) already covers the logic with fakes.

Preconditions:
- The substrate is up and `kubectl get nodes` is Ready.
- PGO is installed (cosmos `requires: [pgo]`); else expect the advisory
  `missing-require` warning and a degraded cosmos.
- `catalog.yaml` (repo root) lists cosmos as entry #1.

Steps:
1. Build: `cd installer && go build -o /tmp/srectl ./cmd/srectl`
2. List the catalog — cosmos appears:
   `/tmp/srectl app list`
   Expect a row: `cosmos  2.102.0  oci:ghcr.io/jongodb-labs/bundles/cosmos  …`
3. Install through the catalog:
   `/tmp/srectl app install cosmos`
   Expect, in order: `resolved cosmos → …@sha256:…`, `signature verified …`,
   any advisory warnings, `deployed cosmos`, `recorded install of cosmos 2.102.0`.
   (If the signature does not verify, the command MUST abort here with the
   expected-identity message and never reach `uds deploy` — fail-closed.)
4. Cohesion wired (the authoritative post-deploy check):
   - `kubectl get virtualservice -A | grep cosmos` → a VirtualService exists.
   - If cosmos declares `sso`: `kubectl -n keycloak get secret keycloak-client-secrets -o yaml | grep cosmos` → a client entry exists.
5. Record written:
   `kubectl -n sre-system get configmap sre-appcatalog-installs -o yaml`
   → a `cosmos:` key with version/source/digest/installedAt/installedBy.
6. Status is green and drift-free:
   `/tmp/srectl app status cosmos`
   → `cosmos: installed`, the record fields, and `live UDS Package: true` with no
   drift note.
7. Drift visibility (optional): `/tmp/srectl app list --installed` shows cosmos
   with `LIVE true` and no drift note.

Teardown (optional): `/tmp/srectl app remove cosmos` → `removed cosmos and pruned
its record`; re-running `app status cosmos` reports `not installed`.

Pass criterion: steps 2–6 succeed as described; the VirtualService (and SSO client
if declared) appear; the record is written; `app status` is green. That is MVP done.

## Worked example #2 — Gitea (first OSS app, via the catalog)

Gitea is the **first off-the-shelf app** onboarded through the exact `srectl app install`
path cosmos defined (mission app #2, catalog entry #2) — chosen specifically because it
exercises every substrate seam cosmos does **not**: declarative SSO, PGO's own generated
Postgres credentials, the MinIO-operator `Tenant` CR, `Package.monitor`, and — for the
first time — a real signed-publish-verify supply chain under a **second signer identity**
(`^https://github.com/JongoDB-Labs/sre-apps/`, vs. cosmos's `cosmos-v2`). Design:
`docs/specs/gitea-onboarding-design.md`. Reference implementation:
**`JongoDB-Labs/sre-apps`** (public, AGPL), `gitea/` — pinned here to release
**`1.27.0-5`**, the revision that passed full lab acceptance end to end (all 6 functional
checks PASS: cohesion, web+SSO button, git push, object storage, `srectl status`,
Prometheus target — `.superpowers/sdd/gitea-onboarding-plan/task-12a-functional-report.md`).
(`catalog.yaml`'s version bump to match ships via a separate, already-open PR — see that
report for the live digest.)

This section covers only what's **different** from the cosmos worked example above —
read §§1–5 first for the shared model.

| Seam | cosmos (app #1) | Gitea (app #2) |
|---|---|---|
| SSO | hand-wired per-org OIDC RP; client created manually | **declarative `Package.sso`** + `secretConfig.template` — zero glue code |
| Postgres | `SUPERUSER` role + app-owned password + migrate-hook role creation | least-priv owner role consuming **PGO's auto-generated `pguser` secret** directly |
| Object storage | bring-your-own MinIO StatefulSet + imperative `mc` init | **MinIO-operator `Tenant` CR** — the substrate's declared object-storage model |
| Monitoring | no `Package.monitor` block | **`Package.monitor`** → auto-created `ServiceMonitor` + scrape netpol |
| Supply chain | bundle never actually published | first end-to-end **publish → keyless sign → verify → catalog install**, second signer identity |

### Declarative SSO — `Package.sso` + `secretConfig.template`

Where cosmos hand-creates its Keycloak client, Gitea's wrapper chart declares one and lets
UDS shape the resulting secret to match exactly what the upstream chart expects
(`sre-apps/gitea/chart/templates/uds-package.yaml`):

```yaml
  sso:
    - name: {{ .Values.sso.name | quote }}
      clientId: {{ .Values.sso.clientId | quote }}
      enabled: true
      standardFlowEnabled: true
      publicClient: false
      redirectUris:
        - {{ printf "https://%s.%s/user/oauth2/keycloak/callback" .Values.host .Values.domain | quote }}
      secretConfig:
        name: {{ .Values.sso.secretName | quote }}
        template:
          # The upstream gitea chart's oauth existingSecret expects EXACTLY
          # the keys "key" (client id) and "secret" (client secret).
          key: "clientField(clientId)"
          secret: "clientField(secret)"
```

`secretConfig.template` is the **key/secret contract**: UDS writes a `gitea-oidc-client`
Secret into the gitea namespace with exactly the two keys the consuming chart names —
`key` and `secret` — via the `clientField(...)` template function, not fixed key names.
Any app whose chart expects differently-named keys just changes the two `template` values;
no code, no glue. The upstream chart then consumes it as an ordinary `existingSecret`:

```yaml
gitea:
  oauth:
    - name: keycloak
      provider: openidConnect
      existingSecret: gitea-oidc-client
      autoDiscoverUrl: https://sso.uds.dev/realms/uds/.well-known/openid-configuration
      scopes: "openid profile email"
```

Acceptance evidence: `kubectl -n gitea get secret gitea-oidc-client -o jsonpath='{.data}'`
had both `key` and `secret` populated, and the login page rendered a working "Sign in with
keycloak" button (`oauth2/keycloak` link, HTTP 200) —
`task-12a-functional-report.md` Checks 1–2.

**Scoped SSO egress.** Gitea is a native OIDC RP (no Authservice), so it resolves
discovery/authorize/token directly against Keycloak — one rule, not cosmos's
`remoteGenerated: Anywhere` caveat:

```yaml
      - description: "SSO — OIDC via tenant gateway"
        direction: Egress
        selector:
          app.kubernetes.io/name: gitea
        remoteNamespace: istio-tenant-gateway
        remoteSelector:
          app: tenant-ingressgateway
        port: 443
```

### Postgres — PGO's own `pguser` secret, `verify-full` over `PGSSLROOTCERT`

cosmos mints its own app password and creates its least-priv role in a migrate hook.
Gitea instead declares a plain PGO user and consumes the secret PGO **already generates**
(`gitea-pg-pguser-gitea`) directly — no SOPS password, no hook:

```yaml
# postgrescluster.yaml
  users:
    # PGO usernames can't contain "_" (DNS-label rule). No SUPERUSER — the
    # declared user is just the owner of its own database (unlike cosmos).
    - name: gitea
      databases: [gitea]
```

```yaml
# values-gitea.yaml
gitea:
  additionalConfigFromEnvs:
    - name: GITEA__DATABASE__PASSWD
      valueFrom:
        secretKeyRef:
          name: gitea-pg-pguser-gitea
          key: password
  config:
    database:
      DB_TYPE: postgres
      HOST: gitea-pg-primary:5432
      USER: gitea
      SSL_MODE: verify-full
deployment:
  env:
    - name: PGSSLROOTCERT           # deployment.env reaches init + main containers, so
      value: /etc/pg-ca/ca.crt      # the configure_gitea initContainer's `gitea migrate` also verifies TLS
```

**This is the generic recipe every future app should copy** — a PGO `users:` entry with
no `SUPERUSER`, its auto-generated `<cluster>-pguser-<user>` secret wired straight into
the app's own env/secret-ref surface, `sslmode=verify-full` against PGO's per-cluster CA
mounted from `<cluster>-cluster-cert`. It worked exactly as designed: acceptance evidence
shows Postgres reached `4/4 Running` and **`verify-full` TLS proven — `gitea migrate`
reached PG over verified TLS** (`fixA-report.md`, rev 1.27.0-5 notes; the same driver
behavior cosmos relies on via `lib/pq`/`PGSSLROOTCERT`, §2 above).

### The PG15+ public-schema pattern — why cosmos went SUPERUSER, and the fix every app needs

**This is the load-bearing finding of the whole exercise.** PostgreSQL 15 revoked the
default `CREATE` privilege on the `public` schema from non-owners. PGO's `users:` block
grants the named role **privileges** on its database, but it does **not** make that role
the database's **owner** — so a plain least-priv PGO user can connect, but any migration
that tries to `CREATE TABLE`/`CREATE EXTENSION` in `public` fails:

```
pq: permission denied for schema public   (SQLSTATE 42501)
```

Gitea hit this live at acceptance (`gitea migrate` failing 42501) — and it is now clear in
hindsight that **this is exactly why the cosmos worked example (§3 above) grants
`SUPERUSER` and creates its own role in a migrate hook**: SUPERUSER was cosmos's
workaround for the same PG15+ ownership gap, not a cosmos-specific requirement. It is not
the generic pattern; it just happened to sidestep the problem cosmos never diagnosed.

**The generic fix, shipped in `sre-apps` gitea rev 1.27.0-5** — PGO's `databaseInitSQL`,
which PGO runs **once, as the Postgres superuser, at cluster init** (before the app's own
role ever connects), to grant real ownership without the app needing superuser at all:

```yaml
# postgrescluster.yaml
spec:
  databaseInitSQL:
    name: gitea-pg-init-sql
    key: init.sql
```

```yaml
# pg-init-sql-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gitea-pg-init-sql
data:
  init.sql: |
    \c gitea
    ALTER DATABASE gitea OWNER TO gitea;
    GRANT ALL ON SCHEMA public TO gitea;
```

**Every future app onboarded onto PGO should ship this ConfigMap + `databaseInitSQL`
pointer by default**, not discover the 42501 the hard way (or reach for `SUPERUSER` as
cosmos did). It is declarative, least-privilege, and runs exactly once per cluster.

### Object storage — MinIO `Tenant` CR + the `mc` bootstrap Job

First real exercise of the substrate's declared object-storage model (`packages/minio`):
a `Tenant` CR provisions the bucket; the app's own bootstrap Job owns IAM (least-priv
access key + policy), per `packages/minio/README.md`'s stated split.

```yaml
# minio-tenant.yaml
apiVersion: minio.min.io/v2
kind: Tenant
metadata:
  name: {{ .Values.minio.tenantName }}
spec:
  configuration:
    name: {{ .Values.minio.rootConfigSecret }}
  image: quay.io/minio/minio:RELEASE.2025-04-08T15-41-24Z   # carried by the substrate
  requestAutoCert: false
  pools:
    - name: pool-0
      servers: 1
      volumesPerServer: 1
      volumeClaimTemplate:
        spec:
          accessModes: ["ReadWriteOnce"]
          resources: { requests: { storage: {{ .Values.minio.storage }} } }
  buckets:
    - name: {{ .Values.minio.bucket }}
```

The idempotent `mc` bootstrap Job (Helm `post-install,post-upgrade` hook) creates the
app-scoped IAM user + bucket policy against the Tenant. **New gotcha: `mc` has no
writable `$HOME` under UDS Core's cluster-wide `runAsNonRoot`/UID-1000 mutation**, no
matter what the chart's own `securityContext` says — it defaulted to
`mkdir /.mc: permission denied` and looped forever. Fix (shipped rev 1.27.0-4):

```yaml
# minio-bootstrap-job.yaml
          env:
            - name: MC_CONFIG_DIR
              value: /tmp/.mc
```

Acceptance evidence: the Tenant reached `Initialized`/`green`; a release-asset attachment
uploaded through Gitea landed in the bucket at the exact path/size
(`attachments/6/3/632ce5a1-...`, 17B) — `task-12a-functional-report.md` Check 4. See
`docs/platform-runbook.md` gotcha #17 for a serious related finding: this Job's own
failure mode (before the fix) tore down an otherwise-fully-healthy Postgres + Tenant via
the release's rollback-on-failure blast radius.

### Monitoring — `Package.monitor`

cosmos ships no `monitor` block at all; Gitea's is the first exercise of the seam:

```yaml
  monitor:
    - selector:
        app.kubernetes.io/name: gitea
      targetPort: 3000
      portName: http
      path: /metrics
      kind: ServiceMonitor
      description: "Gitea metrics"
```

This auto-creates both the `ServiceMonitor` and the Prometheus-scrape netpol — no manual
`allow` rule needed. Confirmed live: `gitea-gitea-metrics` ServiceMonitor present, and
Gitea's own `/metrics` endpoint answered with well-formed Prometheus exposition format
(`task-12a-functional-report.md` Check 6).

### The image-ref identity invariant — crc-tag mechanics

A subtle, generic gotcha every zarf-packaged app must get right: **the image ref the
rendered chart produces must equal the `zarf.yaml` `images:` entry character-for-character**
— tag *and* digest, not just repo+tag. Zarf's mutating agent rewrites pod image refs to
the in-cluster registry by looking up the **exact bundled string** in its image-swap
table (the "crc-tag" it generates, e.g. `...-zarf-2116639277`); if the chart renders a
different string (even a semantically-equivalent one), the lookup misses and the pod pulls
the un-seeded upstream ref instead — silent in a connected lab (it just reaches upstream),
fatal in true airgap.

Gitea hit this: the upstream chart's default `image.digest: ""` renders a **tag-only**
ref, but `zarf.yaml` pins tag **and** digest — the two strings never matched. Fix (rev
1.27.0-5):

```yaml
# values-gitea.yaml
image:
  digest: "sha256:caa57d932c7b78eb19b638dc38fd7c2f5512d4f90d8369c680a73bebf1b1de28"
  rootless: true
```

```yaml
# zarf.yaml
    images:
      - docker.gitea.com/gitea:1.27.0-rootless@sha256:caa57d932c7b78eb19b638dc38fd7c2f5512d4f90d8369c680a73bebf1b1de28
      - docker.io/minio/mc@sha256:eb4ea9884b77704230e2423e9004d2fa738dc272876b9cc41a297d29443b8780
```

Proven identical post-fix: `helm template ... | grep image:` renders
`docker.gitea.com/gitea:1.27.0-rootless@sha256:caa57d93...`, byte-identical to the
`zarf.yaml` entry — asserted permanently in `sre-apps/gitea/tests/render-test.sh`.

### Platform-digest, not index-digest, pinning — the uds-cli remote-deploy bug

A second, sharper consequence of the same "exact bundled ref" mechanics, and the reason
`gitea` shipped three extra revisions (1.27.0-2 → -5) before it deployed cleanly: **pin
bundled images by their linux/amd64 *platform* manifest digest, never by the multi-arch
*index* (manifest-list) digest**, when the bundle may ever be remote-deployed
(`uds deploy oci://...`, not a local tarball).

`uds-cli` v0.34.3's `boci.FindBundledPkgLayers` → `FilterImageIndex` → `getImgManifest`
unconditionally unmarshals each bundled-image descriptor as a single-platform
`ocispec.Manifest`. When the image was pinned by its **index** digest (a manifest-list —
what you get from a plain multi-arch `docker pull`/inspect), the blob is actually an
`ocispec.Index`; `json.Unmarshal` doesn't error, it just silently leaves `Config`/`Layers`
at their zero values, and a later `layer.Digest.Encoded()` call on the resulting empty
descriptor panics:

```
panic: no ':' separator in digest ""
  github.com/defenseunicorns/uds-cli/src/pkg/utils/boci.FindBundledPkgLayers(...)
```

This reproduced identically on both `docker.gitea.com/gitea:1.27.0-rootless` and
`docker.io/minio/mc` (both genuinely multi-arch upstream images), on uds-cli v0.33.0
**and** v0.34.3 (ruling out version skew), and regardless of which of `uds create`'s two
output modes built the bundle — a client-side `uds-cli` defect, unrelated to how the
bundle was published (a competing hypothesis — a missing
`org.opencontainers.image.title` layer annotation — was investigated and conclusively
disproven: the official `defenseunicorns` k3d-core-slim-dev bundle has the identical
"missing annotation" shape and deploys fine). Full investigation, all five disproof steps,
and the source-level root cause: `.superpowers/sdd/gitea-onboarding-plan/fixA-report.md`.

**Fix:** re-pin both images in `zarf.yaml` (and the chart's `image.digest`, per the
invariant above) to their **linux/amd64 platform manifest digest** — the specific child
entry the index already points to — not the index digest itself. Zero trust delta (the
child digest is cryptographically bound inside the already-verified index); the bundle is
amd64-only, so nothing is lost. Shipped as `sre-apps` gitea rev 1.27.0-3. Proven on the
actual published artifact: `uds deploy oci://ghcr.io/jongodb-labs/bundles/gitea:1.27.0-3`
pulls the full 101 MB bundle past the old panic point, failing only at the (absent)
cluster connection.

An upstream `uds-cli` issue for `getImgManifest`'s manifest-vs-index handling is drafted
but **not yet filed** — pending a user decision (`fixA-report.md`, "Follow-ons"). `cosmos`'s
own bundle may share this latent exposure if any of its images are index-digest-pinned and
it is ever remote-deployed (unchecked).

### The two-lane supply chain

Gitea's release pipeline (publish → keyless cosign sign → verify-after-sign gate → SLSA
provenance) is the **open/dev lane** — the same lane cosmos uses, just under a second
signer identity (`^https://github.com/JongoDB-Labs/sre-apps/`). The catalog's fail-closed
authority is always the **registry-level cosign signature**; `uds deploy` itself performs
no signature check of its own (spec §9) — Gitea's `verify.identityRegexp`/`issuer` pair in
`catalog.yaml` is what's actually gating installs, proven by the fail-closed check (a
tampered `identityRegexp` aborted before `uds deploy` ran at all —
`task-12a-report.md` Step 3b). See `docs/product-lineup.md` for the full two-lane trust
model (open/dev vs. gov/CRUCIBLE) this sits inside, and for the admission-time
verification gap that both lanes currently share.
