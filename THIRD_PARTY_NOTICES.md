# Third-party notices

The images built from this repository contain third-party software. The repository's own tooling, adapters, fixtures, schemas, scripts and documentation are MIT ([LICENSE](LICENSE)). Each image carries its own notices at `/licenses/THIRD_PARTY_NOTICES.md` and the licence texts under `/licenses/`.

| Adapter | Upstream | Upstream licence (versions.yaml) | Image licence (Dockerfile label) | Notices |
|---|---|---|---|---|
| `epri` | https://github.com/epri-dev/IEEE-2030.5-Client | BSD-3-Clause | MIT AND BSD-3-Clause | [adapters/epri/THIRD_PARTY_NOTICES.md](adapters/epri/THIRD_PARTY_NOTICES.md) |
| `envoy` | https://github.com/bsgip/envoy | MIT | MIT | [adapters/envoy/THIRD_PARTY_NOTICES.md](adapters/envoy/THIRD_PARTY_NOTICES.md) |
| `py20305` | https://github.com/DERSecurity/py20305 | Apache-2.0 | MIT AND Apache-2.0 | [adapters/py20305/THIRD_PARTY_NOTICES.md](adapters/py20305/THIRD_PARTY_NOTICES.md) |
| `gridappsd` | https://github.com/GRIDAPPSD/ieee-2030_5-server-go, https://github.com/GRIDAPPSD/ieee-2030_5-client-go | LicenseRef-Battelle-BSD | MIT AND LicenseRef-Battelle-BSD | [adapters/gridappsd/THIRD_PARTY_NOTICES.md](adapters/gridappsd/THIRD_PARTY_NOTICES.md) |

Each adapter is a separate program in a separate image. A library that tests against the images talks to them over the network and is not linked to them.
