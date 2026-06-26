# SRE data-service operators

The substrate ships **shared operators**; each mission app provisions its **own
isolated instance** against them. Apps never bundle these — they declare a CR.

| Service | Package | App declares | Isolation |
|---|---|---|---|
| **Postgres** | [`pgo/`](pgo/) — CrunchyData PGO 6.0.2 (cluster-wide) | a `PostgresCluster` CR in its namespace | own DB + pgBackRest backups per app |
| **Object store** | [`minio/`](minio/) — MinIO Operator 7.1.1 (cluster-wide) | a `Tenant` CR in its namespace | own MinIO + buckets per app |

## How an app uses a data-service

PGO is cluster-wide, so a mission app just includes a `PostgresCluster` CR in its
chart (cosmos does — `charts/cosmos/templates/postgrescluster.yaml`). The substrate
already carries the `crunchy-postgres`/`crunchy-pgbackrest` images, so the app's own
package doesn't need them. The app connects over the PGO-issued TLS (mount the cluster
CA, `sslmode=require&sslrootcert=…`) and must allow `KubeAPI` egress in its UDS
`Package` (PGO/Patroni uses the API as its DCS — see the runbook gotcha #11).

## MinIO — the Operator, lab-only (⚠️ upstream EOL — revisit)

Packaged the **MinIO Operator** with per-app `Tenant` CRs, for parity with PGO (shared
operator / isolated data). See [`minio/`](minio/) for the package + a `Tenant` example
(object-lock for WORM).

**⚠️ Caveat that shapes this:** `minio/operator` was **archived 2026-03-20** — MinIO
moved to the commercial AIStor line, so v7.1.1 is the last AGPL release (no upstream
updates). It's pinned and works, and MinIO is **lab/baseline only** (prod swaps in
external FIPS S3 per the install posture), so it's acceptable *for the lab* — but the
object-store choice should be **revisited** (a maintained operator, a shared MinIO server
without the EOL operator, or external S3 even in lab) before it earns a longer life.

Because of that, **cosmos is intentionally NOT migrated onto it yet** — it keeps its
working standalone MinIO StatefulSet. The `Tenant`-CR refactor (buckets incl. the AU-9
object-locked WORM bucket, TLS endpoint, SOPS creds, retention/IAM init Job) is outlined
and **deferred** — we don't couple the app to an archived operator without a deliberate
call.

## Versions

Pinned to what the live reference substrate runs (`docs/platform-runbook.md` T3).
`upstream`/community images for lab; the `registry1` (Iron Bank) flavor swaps in
hardened images for an ATO build.
