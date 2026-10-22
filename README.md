# ieee20305-interop

Independent, reproducible interoperability containers for
[IEEE 2030.5](https://standards.ieee.org/ieee/2030.5/5897/) (Smart Energy
Profile 2.0) implementations.

This repository wraps established third-party IEEE 2030.5 implementations as
Docker containers that all expose the same commands, flags, JSON documents and
exit codes. Any IEEE 2030.5 library, in any language, can then test itself
against real implementation diversity without owning any of it.

## Adapters

| Adapter | Upstream | Language | Role | Licence | Status |
|---|---|---|---|---|---|
| `epri` | [EPRI IEEE 2030.5 Client](https://github.com/epri-dev/IEEE-2030.5-Client) | C | client | BSD-3-Clause | planned |
| `envoy` | [ANU Envoy](https://github.com/bsgip/envoy) | Python | server | MIT | implemented ([README](adapters/envoy/README.md)) |
| `py20305` | [DER Security py20305](https://github.com/DERSecurity/py20305) | Python | client | Apache-2.0 | implemented ([README](adapters/py20305/README.md)) |
| `gridappsd` | [GridAPPS-D server](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) and [client](https://github.com/GRIDAPPSD/ieee-2030_5-client-go) | Go | both | Battelle BSD | implemented ([README](adapters/gridappsd/README.md)) |

## Quick start

```bash
make ci          # validate, lint, build all images and run self-tests
make images      # build the local images
make smoke       # container-contract smoke tests
make interop     # cross-stack tests (every client x every server)
make pki         # throwaway TLS material in ./tls for local work
docker compose up -d --wait   # the reference servers on 127.0.0.1
```

Everything runs with mutual TLS. The tests generate certificates at run time;
none are committed or baked into the images.

## Documentation

- [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) -- the contract
  every image implements
- [docs/CAPABILITIES.md](docs/CAPABILITIES.md) -- what capabilities documents
  describe
- [docs/FIXTURES.md](docs/FIXTURES.md) -- what fixtures seed and send
- [tests/cases.tsv](tests/cases.tsv) -- the cross-stack test ids
- [REQUIREMENTS.md](REQUIREMENTS.md) -- what this repository must do
- [PLAN.md](PLAN.md) -- order of work

## Protocol

IEEE 2030.5 is a RESTful smart energy protocol that uses HTTPS with mutual TLS
(mTLS) and XML payloads. Devices are identified by their Long-Form Device
Identifier (LFDI), derived from their TLS certificate. The protocol defines
resource-based paths (`/dcap`, `/edev`, `/edev/{id}/derp`, etc.) and supports
the Common Smart Inverter Profile (CSIP) and CSIP-AUS for DER management.

## Licence

[MIT](LICENSE). Copyright (c) 2026 OT Fabric.

Each adapter bundles a third-party implementation under its own licence (see
[versions.yaml](versions.yaml)).
