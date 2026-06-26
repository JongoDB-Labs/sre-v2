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
