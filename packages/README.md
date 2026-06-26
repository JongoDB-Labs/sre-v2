# SRE data-service operators

The substrate ships **shared operators**; each mission app provisions its **own
isolated instance** against them. Apps never bundle these — they declare a CR.

| Service | Package | App declares | Isolation |
|---|---|---|---|
| **Postgres** | [`pgo/`](pgo/) — CrunchyData PGO 6.0.2 (cluster-wide) | a `PostgresCluster` CR in its namespace | own DB + pgBackRest backups per app |
| **Object store** | `minio/` *(pending — see below)* | buckets (or a Tenant) | per-app buckets |

## How an app uses a data-service

PGO is cluster-wide, so a mission app just includes a `PostgresCluster` CR in its
chart (cosmos does — `charts/cosmos/templates/postgrescluster.yaml`). The substrate
already carries the `crunchy-postgres`/`crunchy-pgbackrest` images, so the app's own
package doesn't need them. The app connects over the PGO-issued TLS (mount the cluster
CA, `sslmode=require&sslrootcert=…`) and must allow `KubeAPI` egress in its UDS
`Package` (PGO/Patroni uses the API as its DCS — see the runbook gotcha #11).

## MinIO — decision pending

Today MinIO is a **standalone StatefulSet inside the cosmos chart** (not a substrate
service). Two ways to make it a shared data-service, to settle before packaging:

- **MinIO Operator** (per-app `Tenant` CRs) — strongest isolation, matches the
  "shared operator / isolated data" model; heavier; a bigger cosmos refactor.
- **Shared MinIO instance + per-app buckets** — simpler, closest to today's setup
  (just move the StatefulSet to the substrate); the app's init creates its buckets.

For **prod/DoD**, external FIPS S3 replaces MinIO entirely (per the install posture),
so MinIO is the **lab/baseline** object-store only. Leaning toward the operator for
parity with PGO; tracked as the next data-service package + the cosmos MinIO refactor.

## Versions

Pinned to what the live reference substrate runs (`docs/platform-runbook.md` T3).
`upstream`/community images for lab; the `registry1` (Iron Bank) flavor swaps in
hardened images for an ATO build.
