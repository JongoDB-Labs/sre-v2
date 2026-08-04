# Naming Convention

The platform family is named **ARCIS**. Component names follow a single
grammatical rule; new names must pass the test below before they are adopted.

## The rule

`arx, arcis` (f.) is Latin for *citadel* — the fortified high ground of a Roman
city, holding the treasury and the auspices. **ARCIS** is its genitive:
*"of the citadel."*

Every component name is therefore a **nominative noun** that the family name
governs. The full name of any component parses as real Latin — the component
supplies the head noun, ARCIS says whose it is:

| Component | Reads as | Is |
|---|---|---|
| **ARCIS AEGIS** | the shield of the citadel | The runtime substrate (this repo) — UDS-native platform onto which mission apps deploy |
| **ARCIS PROBATIO** | the proving of the citadel | The DSOP pipeline — the vetting examination every artifact passes before admission |
| **ARCIS HORREUM** | the granary of the citadel | Self-hosted OCI registry (Harbor) — scan-on-push, cosign-verify, CVE gating |
| **ARCIS TABULA** | the ledger of the citadel | Compliance evidence & audit store — WORM archive, hash-chained ledger, OSCAL/.ckl emitters |
| **ARCIS VIGILES** | the watchmen of the citadel | Continuous-maintenance loop — rebuild-on-CVE, base-image currency SLAs |
| **ARCIS FABRICA** | the workshop of the citadel | The factory suite — the repo housing PROBATIO tooling, hardened base images, and the HORREUM/TABULA/VIGILES deployments |
| **ARCIS LIMES** | the frontier of the citadel | *(reserved)* CUI-blind egress chokepoint, when extracted from cosmos v2 |

## Test for a new name

A proposed component name is in-family only if **all five** hold:

1. **It is a noun in the nominative.** Latin nominative — singular preferred;
   an attested collective or corps plural is admitted where the institution was
   itself plural (VIGILES — the *Vigiles Urbani* were Rome's watch, always
   named in the plural; cf. the *castra*-class). An unchanged English
   naturalization of such a noun also qualifies (AEGIS, LIMES). Not a verb,
   not an adjective, not an acronym, not a coinage, **not a genitive** — the
   family name already carries the genitive; a second one leaves the phrase
   without a subject.
2. **The genitive construction is true.** `ARCIS <NAME>` must be a factual
   description of the component, not a mood. If "the X of the citadel" doesn't
   describe what the thing does, the name is wrong.
3. **It belongs to the Roman fortification lexicon.** The register is frontier
   defense and military infrastructure — walls, watch, stores, gates, records,
   trials. Not mythology-at-large, not astronomy, not abstractions.
   *Grandfather clause:* **AEGIS** predates the rule (Greek, mythological) and
   is retained; it is not precedent for new names.
4. **It does not overlap an existing member.** One component, one function, one
   word. If two names could describe the same thing, one of them is wrong.
5. **Four syllables or fewer.** Names must stay speakable. This is what
   retired TABULARIUM (five) in favor of TABULA (three).

## Candidate pool for future components

Vetted against the rule; claim as needed.

- **PRAETORIUM** — the commander's HQ at the camp crossroads → control plane
- **PORTA** — the fortified gate → ingress / API gateway
- **SPECULA** — the watchtower → observability stack
- **SACRAMENTUM** — the military oath → signing, attestation, provenance — the
  natural claim for the shared trust fabric when PROBATIO's signing stage is
  extracted as a reusable workflow spanning both trust lanes
- **CASTELLUM** — the fortlet on the frontier line → edge / disconnected deployment
- **VALLUM** — the rampart → *avoid;* redundant against LIMES

## Style

- **ALL CAPS** for family and component names in prose and diagrams.
- **lowercase** in code, repo names, namespaces, and paths: `arcis-aegis`,
  `arcis/horreum`, `namespace: vigiles`.
- The short form **ARX** (nominative) is reserved for the CLI binary and
  environment prefix: `arx up`, `ARX_PROFILE`. Nominative for the tool,
  genitive for the family. Do not use ARX as the family name in prose.
  (`srectl` is the current CLI name; the `srectl → arx` rename is its own
  future slice with a compatibility alias, not a find-and-replace.)
- Pronunciation: *AR-kiss* (classical) or *AR-siss* (anglicized); both are
  accepted, be consistent within a document.

## Out of family

These are deliberately **not** ARCIS names and must never be renamed to fit:

- **Mission apps** (`cosmos`, `gitea`, and successors) are tenants of the
  platform, not parts of it. They keep their own naming.
- **Skins and orgs** ("Pontis", "ĒSO", "defcon") name deployments of an app.
  They name neither the substrate nor the app.

## Retired

- **CRUCIBLE** → **PROBATIO**. *Crucibulum* is medieval metallurgical Latin,
  outside the register; *probatio* was the Roman military entrance examination
  every recruit passed before enlistment — exactly what the gate pipeline is.
- **COLOSSEUM** → **COLOSSUS** → **HORREUM**. A colossus is a monumental
  statue; a *horreum* is the fortified, ventilated, access-controlled,
  inventoried granary of a Roman fort. The registry is a store of signed and
  inspected goods, not a monument. (The deployed Harbor's namespace still lags
  at `colosseum`; live namespaces rename only at natural redeploys.)
- **TABULARIUM** → **TABULA**. Five syllables → three, and sharper: the
  evidence store's core artifact is literally a hash-chained ledger.
- **VIGIL** → **VIGILES**. The attested corps form — the institution was
  plural.
- **ARMARIUM** — dropped without replacement. Domestic furniture, not
  fortification; no component needs the slot.
- Do not write **ArcGIS**. Unrelated Esri product; the spelling is a frequent
  autocorrect target and must be corrected in docs and commit messages.

## Adoption order

Names land in docs first; everything else follows deliberately:

1. This document plus `docs/product-lineup.md` are canonical now.
2. Repo renames are lazy (GitHub redirects make them safe): the factory repo
   (`JongoDB-Labs/aegis`, misnamed since AEGIS moved to the runtime) →
   `fabrica`; this repo → `arcis-aegis` whenever convenient.
3. Live cluster namespaces lag until natural redeploys.
4. `srectl → arx` is its own future slice (alias first).
5. The D2 architecture diagram suite updates in its own docs pass.
