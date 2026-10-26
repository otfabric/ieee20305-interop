# ieee20305-interop Releases

Releases are made by the `Release` workflow when a `vX.Y.Z` tag is pushed.
The section of the version below, up to the `---` that ends it, is the
release's notes.

## How a release is made

1. `make release-prep VERSION=vX.Y.Z` dates the version's section below and
   sets `interop_version` in versions.yaml. Review the notes and commit them
   through a pull request.
2. On the merged `main`, `make release-check VERSION=vX.Y.Z` (and `make ci`)
   verifies it.
3. Tag that commit `vX.Y.Z` and push the tag.

The `Release` workflow checks the repository, builds and self-tests every
image natively on amd64 and arm64 (`.github/workflows/selftest.yml`, the same
jobs as CI), pushes exactly the tested images as per-architecture tags, joins
them into a multi-arch index on GHCR, and creates the GitHub release with
`manifest.json`, which lists the index digests and the upstream pins.
Consumers pin the digests. Every adapter with a Dockerfile must be in the
Makefile's `ADAPTERS`, or the release fails.

The notes are the body of the GitHub release, followed by the image digests:
everything after the heading `## vX.Y.Z` up to the next line that is exactly
`---` (`scripts/release-notes.sh vX.Y.Z` prints it). This is the format of
every otfabric repository and the rule of the shared release workflow, so:
one section per version, newest first, each ending with a `---` line, and no
`---` line inside a section. `make validate` (and CI) fails when RELEASE.md
has no notes for `interop_version`; the `Release` workflow fails, before
anything is built, when it has none for the tag.

---

## v0.2.0

**Date:** 2026-10-23  
**Previous release:** v0.1.0

### Summary

Container contract 0.3 and fixture schema 1.1: subscriptions with
notifications, DER responses, log events, DER resources, flow reservations
and pricing, several programs with default controls, and CSIP-AUS dynamic
operating envelopes, each tested as far as each stack allows.

| Image | Server operations | Client operations | Fixtures |
|-------|-------------------|-------------------|----------|
| `ieee20305-interop-gridappsd` (2018, 2023) | register, discover, poll-controls, report, subscribe, respond, log-event, flow-reservation; `call issue-control` | register, discover, poll-controls, report, respond, log-event, put-der, flow-reservation | baseline, csip |
| `ieee20305-interop-envoy` (2018) | register, discover, poll-controls, report, respond, log-event, put-der, poll-pricing | | baseline, csip-aus |
| `ieee20305-interop-py20305` (2018; 2023 without put-der and poll-pricing) | | register, discover, poll-controls, report, subscribe, respond, log-event, put-der, poll-pricing | baseline, csip, csip-aus (2023: baseline, csip) |
| `ieee20305-interop-epri` (2018) | | register, discover, poll-controls, report, respond | baseline |

### Added

- **Contract 0.3** (`docs/CONTAINER_CONTRACT.md`):
  - `subscribe`: the client's HTTPS notification listener
    (`--notify-listen`, `--notify-host`, and `notify.crt`/`notify.key` in
    its `--tls-dir`), the Subscription it posts, and `ok` once a Notification
    arrives.
  - New client operations `respond`, `log-event`, `put-der`,
    `flow-reservation` and `poll-pricing`. `poll-controls` also checks
    default controls and curves.
  - `call issue-control` on a server, through its control socket, so that a
    test can change what a server serves and make it notify.
  - Server events `device-registered` and `subscription-created`.
  - Capabilities name the fixtures, `call` operations and events of each
    role, and the features `defaultControl`, `curves`, `pricing`,
    `messaging` and `flowReservation`.
- **Fixture schema 1.1** (`docs/FIXTURES.md`): several programs, default
  controls, CSIP-AUS limits (`opModExpLimW`, `opModImpLimW`,
  `opModGenLimW`, `opModLoadLimW`), curves, tariffs, messages, and what a
  client sends for its DER, a log event and a flow reservation. New baked
  fixtures `csip` (two programs with default controls, `opModMaxLimW`,
  `opModFixedW`) and `csip-aus` (dynamic operating envelopes as controls and
  defaults, and a time-of-use tariff).
- **gridappsd**: programs in their own FunctionSetAssignments, default
  controls, `opModFixedW`; subscriptions and notifications, with one
  upstream patch that links the EndDevice's SubscriptionListLink
  (`adapters/gridappsd/patches/0001`); events from sep2server's traffic
  capture; `call issue-control`. The client performs `respond`,
  `log-event`, `put-der` and `flow-reservation`.
- **envoy**: CSIP-AUS limits as controls and as each program's
  DefaultDERControl, `opModFixedW`, several programs, tariffs; `respond`,
  `log-event` and `put-der` served; `device-registered`; self-registered
  devices are in Envoy's default site group, so they get the programs too.
- **py20305**: `subscribe` with py20305's own notification server,
  `respond`, `log-event`, `put-der`, `poll-pricing`, and CSIP-AUS limits,
  default controls and curves in `poll-controls`.
- **epri**: `report` (client_test's own MirrorUsagePoint) and `respond`.
- **Self-tests**: `scripts/interop.sh` runs every baked fixture both ends
  declare, the subscribe test with a named client container the server
  notifies, and the event checks; `scripts/smoke.sh` checks every baked
  fixture, `call` and the new operations; `make_pki` issues the
  notification listener's certificate; `make selftest`.

### Known issues

- Subscriptions pass from py20305 to GridAPPS-D only. The GridAPPS-D client's
  listener needs CCM_8 and a client certificate, which sep2server's delivery
  does not offer; Envoy accepts subscriptions from aggregator certificates
  only; EPRI's client has them commented out.
- No baked fixture has curves (sep2server cannot serve them), and no server
  can be seeded with messages.
- Upstream defects in `tests/known-bugs.tsv`: py20305 writes CSIP-AUS
  extensions in an older namespace than Envoy reads (`put-der`), and does
  not model the `RateComponentLink` that CSIP-AUS 1.3 requires in a
  TimeTariffInterval, so it refuses Envoy's tariffs (`poll-pricing`); EPRI's client crashes on GridAPPS-D's long hrefs,
  sends the draft 2.0.4 namespace, and cannot parse Envoy's EndDevice.
- `client run` is not implemented.

---

## v0.1.0

**Date:** 2026-10-22  
**Previous release:** none

### Summary

First release: four IEEE 2030.5 implementations as containers behind one
contract (draft 0.2), with mutual TLS everywhere and self-tests of every
client against every server.

| Image | Upstream | Role | IEEE 2030.5 |
|-------|----------|------|-------------|
| `ieee20305-interop-gridappsd` | GridAPPS-D server v0.18.0 and client v0.1.0 (Go) | server, client | 2018, 2023 |
| `ieee20305-interop-envoy` | ANU Envoy v2.5.0 (Python, CSIP-AUS) | server | 2018 |
| `ieee20305-interop-py20305` | DER Security py20305 v0.16.0 (Python) | client | 2018, 2023 |
| `ieee20305-interop-epri` | EPRI IEEE 2030.5 Client v0.2.11 (C) | client | 2018 |

### Added

- **Container contract 0.2** (`docs/CONTAINER_CONTRACT.md`): `server`,
  one-shot `client <operation>` (`register`, `discover`, `poll-controls`,
  `report`), result documents, events, capabilities with TLS cipher suites,
  exit codes. Servers are seeded by the fixture and provision the devices of
  `--tls-dir/devices`.
- **The baseline fixture**: one device, a DER program with two scheduled
  connect/energize controls, and a metering report; schema and checks
  (`make fixtures`).
- **Self-tests**: `scripts/smoke.sh` per image and `scripts/interop.sh`
  for every client against every server, with a PKI generated per run
  (IEEE 2030.5 certificate profile), refusal tests (a device of another CA,
  an untrusted server, an unprovisioned device), and
  `tests/known-bugs.tsv`, whose stale entries fail the run.
- **Pins**: every upstream at a release and its commit, base images by
  digest, Debian packages by version, Python dependencies from hashed locks;
  `make versions` checks `versions.yaml` against the Dockerfiles.
- **Workflows**: CI (validation and the self-tests on amd64 and arm64), the
  weekly `Upstream candidates` probe, and `Release`.
- `make pki` and `compose.yaml` for running the servers locally.

### Known issues

- The EPRI client crashes polling GridAPPS-D's controls (a 64-byte URI
  buffer overflows on its long hrefs) and cannot parse Envoy's EndDevice
  (the CSIP-AUS element `csipaus:ConnectionPointLink`). Both are in
  `tests/known-bugs.tsv`.
- No adapter implements `subscribe`, `client run` or `call` yet.

---
