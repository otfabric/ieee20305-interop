# Plan

The order of work for this repository. The overall plan, which also covers
otfabric/go-ieee20305, is in that repository's IMPLEMENTATION_PLAN.md.

## Phase 0 - Design (done, 2026-10-22)

- [REQUIREMENTS.md](REQUIREMENTS.md)
- [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md), draft 0.1 (now 0.2)
- Document schemas: capabilities, fixtures
- [versions.yaml](versions.yaml) with the candidate upstreams (status `planned`)

## Phase 1 - Foundation (in progress)

- [x] Layout, licence, Makefile, CI (validation job)
- [x] Fixture schema and the `baseline` fixture: one device, a DER
      program with controls, and metering
- [x] versions.yaml with the candidate upstreams (status `planned`)
- [x] Schema validators, shellcheck
- [x] Shell scripts: lib.sh, smoke.sh, interop.sh, retry.sh
- [ ] CI green on GitHub
- [ ] Release workflow

## Phase 2 - First adapters (in progress)

- [x] `gridappsd` adapter: Go server and client, 2018 and 2023, CCM_8 only
- [x] Dockerfile: pinned bases and upstream commits, verified modules,
      non-root, licences, OCI labels, health check
- [x] Self-tests: smoke tests per image, then every client against every
      server, with TLS material generated per run
- [x] Contract 0.2: mutual TLS everywhere, servers seeded by the fixture,
      TLS capabilities
- [x] `py20305` adapter: Python client (v0.16.0), GCM and CCM_8, with a
      hashed dependency lock
- [x] `envoy` adapter: Python server (v2.5.0) with PostgreSQL and HAProxy,
      which forwards the client certificate's fingerprint
- [ ] `epri` adapter: C client, CCM_8 only
- [ ] Control socket and `call`, `client run`, subscriptions, and the events
- [ ] Contract 1.0, once the adapters close the gaps

## Phase 3 - Profiles and versions (open)

- [ ] CSIP profile support across adapters
- [ ] CSIP-AUS profile (envoy's strength)
- [ ] IEEE 2030.5-2023 additions where upstreams support them
- [ ] Subscription and notification testing
