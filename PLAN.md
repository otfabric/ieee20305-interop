# Plan

The order of work for this repository. The overall plan, which also covers
otfabric/go-ieee20305, is in that repository's IMPLEMENTATION_PLAN.md.

## Phase 0 - Design (done, 2026-10-22)

- [REQUIREMENTS.md](REQUIREMENTS.md)
- [docs/CONTAINER_CONTRACT.md](docs/CONTAINER_CONTRACT.md), draft 0.1 (now 0.2)
- Document schemas: capabilities, fixtures
- [versions.yaml](versions.yaml) with the candidate upstreams (status `planned`)

## Phase 1 - Foundation (done)

- [x] Layout, licence, Makefile, CI (validation job)
- [x] Fixture schema and the `baseline` fixture: one device, a DER
      program with controls, and metering
- [x] versions.yaml with the candidate upstreams (status `planned`)
- [x] Schema validators, shellcheck
- [x] Shell scripts: lib.sh, smoke.sh, interop.sh, retry.sh
- [x] CI green on GitHub
- [x] Release workflow (first release v0.1.0, 2026-10-22)

## Phase 2 - Adapters (in progress)

- [x] `gridappsd` adapter: Go server and client, 2018 and 2023, CCM_8 only
- [x] Dockerfile: pinned bases and upstream commits, verified modules,
      non-root, licences, OCI labels, health check
- [x] Self-tests: smoke tests per image, then every client against every
      server, with TLS material generated per run
- [x] Contract 0.2: mutual TLS everywhere, servers seeded by the fixture,
      TLS capabilities
- [x] `py20305` adapter: Python client (v0.16.2 since v0.4.1), GCM and CCM_8, with a
      hashed dependency lock
- [x] `envoy` adapter: Python server (v2.5.0) with PostgreSQL and HAProxy,
      which forwards the client certificate's fingerprint
- [x] `epri` adapter: C client, CCM_8 only, with one patch (SHA-256
      padding); its known upstream issues are in
      [tests/known-bugs.tsv](tests/known-bugs.tsv)
- [x] Contract 0.3: subscriptions and notifications, `call issue-control`,
      the server events, respond, log-event, put-der, flow-reservation,
      poll-pricing; fixture schema 1.1 with the `csip` and `csip-aus` fixtures
- [x] OT Fabric's own stack (ADR-0012): `own/go-ieee20305`, built by hand
      with the workflow "Own stack (go-ieee20305)", not independent and not in
      a release (v0.4.0)
- [x] Upstream tracking: the daily Upstream workflow and issue #2; every
      upstream on its latest release (v0.4.1)
- [ ] `client run` and the client events
- [ ] Contract 1.0, once the adapters close the gaps

## Phase 3 - Profiles and versions (open)

- [x] CSIP profile: several programs, default controls (`csip` fixture)
- [x] CSIP-AUS profile: dynamic operating envelopes and tariffs (`csip-aus`)
- [ ] Curves and messaging: no server adapter serves them yet
- [ ] IEEE 2030.5-2023 additions where upstreams support them
- [x] Subscription and notification testing (py20305 to GridAPPS-D)
