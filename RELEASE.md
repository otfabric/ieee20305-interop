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
