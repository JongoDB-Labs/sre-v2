# SRE-v2 Architecture Diagram Suite

A maintainable, diagram-as-code suite (D2) describing the **SRE-v2 substrate**, how
**cosmos-v2 and any compatible mission app** deploy on top of it, and the
**DevSecOps / GitOps delivery model** that ships and updates them.

Design spec: [`../specs/architecture-diagram-suite-design.md`](../specs/architecture-diagram-suite-design.md)

## Views

| # | Source | Rendered | What it shows |
|---|--------|----------|---------------|
| ① | [`01-system-of-systems.d2`](01-system-of-systems.d2) | [svg](rendered/01-system-of-systems.svg) | Overview poster: repos → CI → GHCR → delivery gate → substrate → app → environments |
| ② | [`02-substrate-internals.d2`](02-substrate-internals.d2) | [svg](rendered/02-substrate-internals.svg) | SRE-v2 substrate internals (UDS Core layers, operators, srectl/catalog) |
| ③ | [`03-app-on-substrate.d2`](03-app-on-substrate.d2) | [svg](rendered/03-app-on-substrate.svg) | The app-on-substrate contract (cosmos-v2 as reference) |
| ④ | [`04-devsecops-supply-chain.d2`](04-devsecops-supply-chain.d2) | [svg](rendered/04-devsecops-supply-chain.svg) | DevSecOps pipeline + supply chain (gates, SBOM, sign, SLSA, verify) |
| ⑤ | [`05-gitops-delivery.d2`](05-gitops-delivery.d2) | [svg](rendered/05-gitops-delivery.svg) | GitOps (connected) vs airgap (Zarf/UDS) vs today; update gate + Day-2 |
| ⑥ | [`06-environments.d2`](06-environments.d2) | [svg](rendered/06-environments.svg) | Environments & promotion — honest current vs target |

Shared legend / theme: [`_theme.d2`](_theme.d2)

## Rendering

```sh
# install once:  brew install d2   (or: curl -fsSL https://d2lang.com/install.sh | sh -s --)
./render.sh          # renders every *.d2 to rendered/*.svg (and *.png)
```

Rendered SVG/PNG are committed so the diagrams show in GitHub and drop straight
into an ATO/onboarding package without a toolchain.
