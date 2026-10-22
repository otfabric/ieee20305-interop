# envoy adapter

Wraps [Envoy](https://github.com/bsgip/envoy), the IEEE 2030.5 / CSIP-AUS
server of the Battery Storage and Grid Integration Program (Australian
National University), pinned to v2.5.0 (`9d00340b…`) under MIT. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The pins are in the
[Dockerfile](Dockerfile) and [versions.yaml](../../versions.yaml).

Server only, IEEE 2030.5-2018: `client` validates its flags and exits 5.

## Build and run

```bash
make images ADAPTERS=envoy
make pki
docker compose up -d --wait envoy-server   # https://127.0.0.1:8445/dcap
```

The image installs Envoy from the pinned source, its dependencies from
Envoy's own `uv.lock` (exported with its hashes), and the build tools from
[build-requirements.txt](build-requirements.txt) (`make lock-envoy`).
HAProxy and PostgreSQL 17 are Debian trixie packages at pinned versions.

## How the adapter works

Envoy is deployed the way its own demo deploys it, in one container, all
of it as uid 10001 and all on loopback except the TLS proxy:

1. **PostgreSQL**, initialised in `/tmp`, and migrated by Envoy's own
   Alembic scripts.
2. **Devices.** Each certificate of `--tls-dir/devices` becomes a site of
   Envoy's NULL aggregator, with its LFDI, SFDI, the fixture's device
   category and PIN, inserted through Envoy's models and CRUD layer, as
   Envoy's demo seeds its database. Envoy's admin API has no way to create a
   site.
3. **Envoy** and **Envoy admin** under uvicorn, with
   `ALLOW_DEVICE_REGISTRATION=true` (device certificates are accepted; an
   unknown device can register itself) and a random admin password.
4. **Programs and controls**, through the admin API: a site group of the
   provisioned sites; per DER program a site control group (description,
   primacy, FSA 1); per control a site control for that site group, with
   `set_connect` / `set_energized`, its start and duration.
5. **HAProxy** on `--port`: TLS 1.2 with mutual TLS against
   `--tls-dir/ca.crt`, refusing a bad client certificate in the handshake,
   and forwarding the client certificate's SHA-256 fingerprint in
   `x-forwarded-client-cert`, which Envoy turns into the LFDI.

Then it writes `device-provisioned` and `ready`. On SIGTERM it stops all of
it, in reverse order, and writes `stopped`. The processes log to stderr.

## Limits

- Fixtures: DERControlBase `opModConnect` and `opModEnergize` only, and no
  `defaultControl`: Envoy's controls and defaults otherwise speak CSIP-AUS
  watt limits, which the fixtures do not express. Anything else is exit 5.
- Envoy describes a control by its start time, not by the fixture's
  description; clients match controls by what they do.
- Envoy serves the registration PIN as five stored digits followed by their
  digit sum modulo 10, while it computes the SFDI check digit so that the
  digit sum is a multiple of 10. A fixture PIN Envoy would serve differently
  is exit 5; the baseline PIN reads the same under both rules.
- `subscribe` and `call` are not implemented (exit 5).
