# Compatibility

Which upstream versions the adapters are built on, what the reference stacks
were verified to do against each other, and what was found on the way.

Everything here is about the reference stacks. It makes no statement about
any library that consumes the images, nor about OT Fabric's own stack
(`own/go-ieee20305/README.md`), which is not a reference.

## Pinned upstreams

The rule is the latest upstream release, also when it has a bug: the bug is
listed in [tests/known-bugs.tsv](../tests/known-bugs.tsv) and described
below, and the cases it explains count as known issues instead of failures.

| Adapter | Upstream | Pinned | Pin | Engine |
|---------|----------|--------|-----|--------|
| `epri` | [EPRI IEEE 2030.5 Client](https://github.com/epri-dev/IEEE-2030.5-Client) | v0.2.11 | commit `505ee47d9c3ed43ce63087198d09f77338da627d`, one patch | independent, C |
| `envoy` | [ANU Envoy](https://github.com/bsgip/envoy) | v2.5.0 | commit `9d00340b5592f1fdf09dc74bd743fc251da0bfb5`, dependencies from its `uv.lock` | independent, Python |
| `py20305` | [DER Security py20305](https://github.com/DERSecurity/py20305) | v0.16.2 | commit `9b6e75c7f9f03a110c73c574f6072bf0320a0f3a`, hashed lock of this repository | independent, Python |
| `gridappsd` | [GridAPPS-D server](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) | v0.18.0 | commit `d7a4a17042af7506610a973c6be8a5466db34053`, one patch | independent, Go |
| | [GridAPPS-D client](https://github.com/GRIDAPPSD/ieee-2030_5-client-go) | v0.1.0 | commit `66570b94ea104b46eb12b48ef2451b3454050176` | |

The pins live in the Dockerfiles and are mirrored in
[versions.yaml](../versions.yaml); `scripts/check-versions.sh` keeps them in
step. The **Upstream** workflow (`.github/workflows/upstream.yml`) resolves
the latest release of each stack (`scripts/latest-upstream.py`), builds the
adapters on those newer than the pin and self-tests them. It never changes a
pin.

## Cross-stack matrix

`make interop` runs every case of [tests/cases.tsv](../tests/cases.tsv), for
each IEEE 2030.5 edition, fixture and pairing that both ends declare
([docs/CAPABILITIES.md](CAPABILITIES.md)), over a TLS cipher suite both
offer. Each cell counts the cases of a client against a server, over every
fixture both declare:

| Client → Server | gridappsd 2018 | envoy 2018 | gridappsd 2023 |
|-----------------|:--------------:|:----------:|:--------------:|
| **gridappsd** | 17 pass | 10 pass | 17 pass |
| **py20305** | 19 pass | 19 pass | 19 pass |
| **epri** | 4 pass, 3 known ¹ | 3 pass, 4 known ¹ | (2018 only) |

Envoy serves IEEE 2030.5-2018 only, and EPRI's client speaks it only. What a
pairing does not declare is skipped and counted apart (`SKIP_UNSUPPORTED`).

¹ [EPRI client](#epri-client-a-list-href-longer-than-63-characters) findings
below.

Verified on linux/arm64 with the pins above on 2026-11-06 (local run of this
release's images, 323 passed, 0 failed, 11 known, together with OT Fabric's
own image, whose pairings are not shown here), and by CI on linux/amd64 and
linux/arm64.

## Findings

Each is an entry of [tests/known-bugs.tsv](../tests/known-bugs.tsv), with its
evidence. A run fails when an entry no longer reproduces.

### EPRI client: a list href longer than 63 characters

`get_seq()` formats a list href and its query into `char uri[64]`. The
DERControlList href of GridAPPS-D (about 70 to 100 characters) overflows it,
and `client_test` crashes (Bus error) before it reads a control or responds
to one: `poll-controls` and `respond` against GridAPPS-D.

### EPRI client: the draft name space

`client_test` writes every document in the name space of the draft sep.xsd
2.0.4, `http://ieee.org/2030.5`, not `urn:ieee:std:2030.5:ns` of IEEE
2030.5-2018. A server that reads the standard's name space refuses what it
posts with 400: `report` against GridAPPS-D.

### EPRI client: CSIP-AUS extension elements

`client_test` parses strictly by its sep.xsd 2.0.4 and rejects the CSIP-AUS
element `csipaus:ConnectionPointLink` that Envoy adds to EndDevice, so it gets
no further than the EndDeviceList against Envoy.

### EPRI client: deviceCategory

`client_test` knows the EndDevice of its sep.xsd 2.0.4, whose device
category is `loadShedDeviceCategory`
([se_schema.c](https://github.com/epri-dev/IEEE-2030.5-Client/blob/505ee47d9c3ed43ce63087198d09f77338da627d/se_schema.c#L2166)).
It rejects an EndDeviceList whose EndDevice carries the `deviceCategory` of
IEEE 2030.5-2018, as a server that serves the fixture's device category
sends it (OT Fabric's own server), and gets no further. GridAPPS-D serves no
device category, and Envoy's EndDevice is rejected before for its CSIP-AUS
element.

### Fixed upstream: py20305's CSIP-AUS name space and RateComponentLink

py20305 v0.16.0 wrote the CSIP-AUS extensions in `https://csipaus.org/ns`,
which Envoy (`https://csipaus.org/ns/v1.3`) refused (`put-der`), and did not
model the RateComponentLink of a CSIP-AUS 1.3 TimeTariffInterval
(`poll-pricing` of `csip-aus`). Both were fixed in v0.16.2
([DERSecurity/py20305#80](https://github.com/DERSecurity/py20305/pull/80)),
the pin since ieee20305-interop v0.4.1, and their entries are gone from
`tests/known-bugs.tsv`.

### What no pairing exercises

See [docs/CAPABILITIES.md](CAPABILITIES.md#declared-now): curves and
messaging are seeded by no server adapter, and `put-der`,
`flow-reservation` and `poll-pricing` have a single server each.
