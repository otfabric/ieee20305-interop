# Plan

The order of work for this repository. The overall plan, which also covers
otfabric/go-ieee20305, is in that repository's IMPLEMENTATION_PLAN.md.

## Phase 0 - Design (done, 2026-10-22)

- [REQUIREMENTS.md](REQUIREMENTS.md)
- [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md), draft 0.1
- Document schemas: capabilities, fixtures
- [versions.yaml](versions.yaml) with the candidate upstreams (status `planned`)

## Phase 1 - Foundation (in progress)

- [x] Layout, licence, Makefile, CI (validation job)
- [x] Fixture schema and the `baseline` fixture with EndDevices, DER
      programs, controls and test certificates
- [x] versions.yaml with the candidate upstreams (status `planned`)
- [x] Schema validators, shellcheck
- [x] Shell scripts: lib.sh, smoke.sh, interop.sh, retry.sh
- [ ] CI green on GitHub
- [ ] Release workflow

## Phase 2 - First adapters (open)

- [ ] `envoy` adapter: Python/FastAPI server, CSIP-AUS profile
- [ ] `epri` adapter: C client, device registration and control polling
- [ ] `py20305` adapter: Python client, LFDI-based identity
- [ ] `gridappsd` adapter: Go server and client
- [ ] Control socket and `call`, `client run`, and the events
- [ ] Dockerfiles: pinned bases, hashed dependency locks, non-root,
      licences, OCI labels, health check
- [ ] Self-tests: smoke tests per image, then every client against every
      server with requests from both ends
- [ ] Contract 0.2: what implementing it with these upstreams shows
- [ ] Contract 1.0, once the adapters close the gaps

## Phase 3 - Profiles and versions (open)

- [ ] CSIP profile support across adapters
- [ ] CSIP-AUS profile (envoy's strength)
- [ ] IEEE 2030.5-2023 additions where upstreams support them
- [ ] Subscription and notification testing
