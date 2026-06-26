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
> This toil is exactly what the **installer/Day-2 wizard must absorb** (`srectl` seeds
> the platform admin + users/service-creds). Also confirm the realm permits password
> login — the `uds` realm carries a `DENY_USERNAME_PASSWORD` hardening flag.

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
