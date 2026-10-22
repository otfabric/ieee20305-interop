# Requirements

This file says what this repository must and must not do.
[PLAN.md](PLAN.md) gives the order of the work.
[docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md) and
[docs/FIXTURES.md](docs/FIXTURES.md) are the specifications that follow from
these requirements.

## Purpose

This repository provides independent, reproducible interoperability tests
for implementations of the IEEE 2030.5 (Smart Energy Profile 2.0) protocol.
It packages established third-party IEEE 2030.5 implementations as containers
that all behave the same way. Any IEEE 2030.5 library, in any language, can
then test itself against real implementation diversity without owning any of it.

It is not an IEEE 2030.5 implementation, not a utility aggregator, and not
a substitute for IEEE conformance testing.

## R1 - Repository isolation

- R1.1 This repository does not import, build, link against or depend on any
  library under test. It contains no test logic or expectation specific to
  one.
- R1.2 A consumer depends only on the published images and the documented
  contract. It does not need a checkout of this repository.
- R1.3 Each adapter drives its own upstream IEEE 2030.5 stack. Adapter glue
  (argument parsing, fixture loading, JSON output) is allowed. A shared
  IEEE 2030.5 engine that replaces part of an upstream stack is not.

| Responsibility | Here | Consumer |
|----------------|:----:|:--------:|
| Third-party implementations, adapters, Dockerfiles | yes | no |
| Fixtures, fixture schema and validation | yes | no |
| Container contract, capability declarations | yes | no |
| Image builds and publishing, licence compliance | yes | no |
| Self-tests of the reference stacks against each other | yes | no |
| Scenarios and assertions about a library under test | no | yes |
| Compatibility claims of a library under test | no | yes |

## R2 - Reference implementations

| Adapter | Upstream | Language | Roles | Priority |
|---|---|---|---|---|
| `epri` | [EPRI IEEE 2030.5 Client](https://github.com/epri-dev/IEEE-2030.5-Client) | C | client | P0 |
| `envoy` | [ANU Envoy](https://github.com/bsgip/envoy) | Python | server | P0 |
| `py20305` | [DER Security py20305](https://github.com/DERSecurity/py20305) | Python | client | P0 |
| `gridappsd` | [GridAPPS-D](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) | Go | server, client | P0 |

- R2.1 Support that an upstream advertises is a candidate, not a fact. A
  capability is declared only after a self-test exercises it (R4).
- R2.2 Every reference is pinned to a release and a commit (R6).

## R3 - One contract

- R3.1 Every adapter image implements
  [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md): the same
  commands, flags, documents and exit codes.
- R3.2 The contract is versioned. Breaking changes bump the major version;
  additions bump the minor version.
- R3.3 Every document an image writes validates against its schema in
  [schemas/](schemas).

## R4 - Honest capabilities

- R4.1 `print-capabilities` declares capabilities per protocol version, per
  profile and per role. A feature supported in one version is not assumed in
  another.
- R4.2 A capability is `true` only if the self-tests exercise it.
- R4.3 A scenario an adapter cannot run is reported as `SKIP_UNSUPPORTED`,
  never as `PASS`.
- R4.4 A known upstream defect is recorded in `tests/known-bugs.tsv` with a
  link to evidence. A known-bug entry that no longer reproduces fails the
  run, so that the list cannot go stale.

## R5 - Self-tests

- R5.1 Each image passes its own smoke tests before it is used against
  anything else.
- R5.2 Every client adapter runs against every server adapter, for every
  version both support, testing device registration, DER program discovery,
  control polling and metering reports.
- R5.3 The tests run deterministically and in isolation. No host port is
  published; no state leaks between runs.
- R5.4 Only baked fixtures and test-generated mTLS certificates are used.
  No test depends on an external server, a registry pull or a network call
  at test time (image pulls go through retry.sh).

## R6 - Reproducible builds

- R6.1 Every upstream is pinned to a release and a commit.
- R6.2 Every base image is pinned by digest.
- R6.3 Every dependency is locked by version and hash where the ecosystem
  supports it.
- R6.4 `versions.yaml` mirrors every pin; `make versions` fails when they
  disagree.

## R7 - Security

- R7.1 No adapter runs as root.
- R7.2 Each image bundles its upstream's licence and a third-party notices
  file under `/licenses`.
- R7.3 No private key is committed or baked into an image. mTLS test
  scenarios generate device certificates and CA hierarchies at test time and
  mount them read-only.
- R7.4 The baseline fixture's device identities and test certificates are
  public by design. They must never be used against a production system.

## R8 - IEEE 2030.5 specifics

- R8.1 The protocol uses HTTPS with mutual TLS. Device identity is derived
  from the TLS certificate (LFDI = SHA-256 of the certificate, truncated to
  160 bits; SFDI = first 36 bits of the LFDI).
- R8.2 Resources are XML, following the IEEE 2030.5 XSD schema. The contract
  accepts and returns JSON for test orchestration, while the adapters handle
  XML on the wire.
- R8.3 Fixtures describe EndDevices, DER programs, DERControls, metering
  readings and test certificate material (CA chain, device certificates).
- R8.4 Operations: `register` (POST EndDevice), `discover` (GET
  DeviceCapability, DERProgramList), `poll-controls` (GET DERControlList),
  `report` (POST MirrorUsagePoint), `subscribe` (POST Subscription).
