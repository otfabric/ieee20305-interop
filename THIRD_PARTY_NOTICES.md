# Third-party notices

The images built from this repository contain third-party software. The repository's own tooling, adapters, fixtures, schemas, scripts and documentation are MIT ([LICENSE](LICENSE)). Each image carries its own notices at `/licenses/THIRD_PARTY_NOTICES.md` and the licence texts under `/licenses/`.

| Adapter | Upstream | Upstream licence (versions.yaml) | Image licence (Dockerfile label) | Notices |
|---|---|---|---|---|
| `epri` | https://github.com/epri-dev/IEEE-2030.5-Client | BSD-3-Clause | MIT AND BSD-3-Clause | [adapters/epri/THIRD_PARTY_NOTICES.md](adapters/epri/THIRD_PARTY_NOTICES.md) |
| `envoy` | https://github.com/bsgip/envoy | MIT | MIT | [adapters/envoy/THIRD_PARTY_NOTICES.md](adapters/envoy/THIRD_PARTY_NOTICES.md) |
| `py20305` | https://github.com/DERSecurity/py20305 | Apache-2.0 | MIT AND Apache-2.0 | [adapters/py20305/THIRD_PARTY_NOTICES.md](adapters/py20305/THIRD_PARTY_NOTICES.md) |
| `gridappsd` | https://github.com/GRIDAPPSD/ieee-2030_5-server-go, https://github.com/GRIDAPPSD/ieee-2030_5-client-go | LicenseRef-Battelle-BSD | MIT AND LicenseRef-Battelle-BSD | [adapters/gridappsd/THIRD_PARTY_NOTICES.md](adapters/gridappsd/THIRD_PARTY_NOTICES.md) |

| `otfabric` (`own/go-ieee20305/`, not a reference) | https://github.com/otfabric/go-ieee20305 | MIT | MIT AND BSD-3-Clause AND LicenseRef-Battelle-BSD | below |

Each adapter is a separate program in a separate image. A library that tests against the images talks to them over the network and is not linked to them.

## Image `ieee20305-interop-otfabric`

OT Fabric's own stack (`own/go-ieee20305/README.md`), not part of a release.

| Component | Version | Licence | Source |
|-----------|---------|---------|--------|
| go-ieee20305 | latest release when built | MIT | https://github.com/otfabric/go-ieee20305 |
| go-ieee20305/tlsccm, with its copy of Go's `crypto/tls` and Pion's AES-CCM | the version go-ieee20305 needs | MIT, BSD-3-Clause, LicenseRef-Battelle-BSD, MIT (Pion) | https://github.com/otfabric/go-ieee20305/tree/main/tlsccm |
| golang.org/x/crypto, golang.org/x/sys | as go.mod resolves them | BSD-3-Clause | https://go.googlesource.com |
| Go standard library | 1.26.8 | BSD-3-Clause | https://go.dev |
| Adapter | this repository, `own/go-ieee20305/` | MIT | https://github.com/otfabric/ieee20305-interop |
| Base system | Debian trixie-slim | various; see `/usr/share/doc/*/copyright` in the image | https://www.debian.org |

The image keeps every licence and notice file of each module (including those
under tlsccm's `internal/`) in `/licenses/<module>/`.
