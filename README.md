# ARCIS AEGIS — the Secure Runtime Environment

> *"the shield of the citadel"* — the runtime plane of the **ARCIS** platform
> family ([family hub](https://github.com/JongoDB-Labs/arcis)). Repo renamed
> `sre-v2` → `arcis-aegis` 2026-08-04; old URLs redirect.

A UDS-native, DoD-compliant **runtime substrate** for hosting mission applications.
SRE *is* the platform — ingress, identity, observability, data operators, secrets,
and lifecycle tooling — onto which business apps are deployed as UDS mission
packages. The first app is **cosmos** ([cosmos-v2](https://github.com/JongoDB-Labs/cosmos-v2));
SRE itself is app-agnostic.

> **Naming:** ARCIS AEGIS is the *substrate*; `cosmos`/`gitea` are *apps*
> (tenants, out-of-family); "Pontis", "ĒSO", "defcon" are **skins / orgs** using
> the app. Full convention: [arcis/naming.md](https://github.com/JongoDB-Labs/arcis/blob/main/naming.md).

## Two layers

**1 · Substrate (this repo)** — stood up once (round 1) by the installer:
UDS Core (Istio ingress · Keycloak SSO · Falco runtime-security · Prometheus/Grafana)
\+ **CrunchyData PGO** and **MinIO** operators + SOPS secrets + sizing/posture.

**2 · Mission apps (separate repos)** — deployed onto a running SRE (round 2) from
local / OCI / GitHub. Each app is a signed UDS **Package** that plugs into SRE's
shared services through its CR:

| Cohesion | Mechanism |
|---|---|
| **Ingress** | `Package.expose` → a VirtualService on the shared Istio gateway |
| **Auth / IdP** | `Package.sso` → a Keycloak client → cohesive SSO across *every* app |
| **Observability** | `Package.monitor` → Prometheus + Grafana |
| **Network** | `Package` default-deny + allow-list (incl. `KubeAPI` where needed) |
| **Data** | the app requests its own PGO `PostgresCluster` + MinIO buckets |

**Shared operators, isolated per-app data.**

## Layout

| Path | Holds |
|---|---|
| `bundle/` | the SRE UDS bundle — init + UDS Core layers + PGO/MinIO operators |
| `installer/` | the Security-Onion-style **TUI/CLI** (host → cluster → SRE → first app), re-entrant for Day-2 |
| `packages/` | Zarf packages for the platform operators (PGO, MinIO) |
| `docs/` | platform bring-up runbook + the gotcha catalog + troubleshooting |

## Status

Bootstrapped 2026-06-26 by splitting the substrate out of `cosmos-v2`. A live
reference deployment (RKE2 + full UDS Core, 12 vCPU / 62 GiB) already runs and is
documented in `cosmos-v2/docs/deploy/uds-rke2-setup.md` (migrating into `docs/`).
See [`docs/MIGRATION.md`](docs/MIGRATION.md) for what's moving and the build order.

---

## License

Licensed under the **GNU Affero General Public License v3.0** — see
[LICENSE](LICENSE) for the full text.

AGPL-3.0 is a network copyleft license: if you modify this software and make it
available to users over a network, you must also offer those users the
corresponding source. For alternative licensing terms, open an issue to start a
conversation.
