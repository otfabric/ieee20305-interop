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
4. **Programs, defaults, controls and tariffs**, through the admin API: a
   site group of the provisioned sites, which is Envoy's default group, so
   that a device that registers itself joins it too; per DER program a site
   control group (description, primacy, FSA 1), its `defaultControl` as the
   group's default (CSIP-AUS limits) and per control a site control for the
   site group, with its start and duration; per tariff a tariff, per rate
   component a tariff component and per time tariff interval a generated
   rate for the site group.
5. **HAProxy** on `--port`: TLS 1.2 with mutual TLS against
   `--tls-dir/ca.crt`, refusing a bad client certificate in the handshake,
   and forwarding the client certificate's SHA-256 fingerprint in
   `x-forwarded-client-cert`, which Envoy turns into the LFDI.

Then it writes `device-provisioned` and `ready`. While it runs, it lists
Envoy's sites through the admin API every 0.5 s and writes
`device-registered` for each site it did not provision (Envoy logs a
registration before it inserts the site, and also when it refuses one). On
SIGTERM it stops all of it, in reverse order, and writes `stopped`. The
processes log to stderr.

## What it declares

IEEE 2030.5-2018, server: `register`, `discover`, `poll-controls`, `report`,
`respond`, `log-event`, `put-der`, `poll-pricing`; fixtures `baseline` and `csip-aus`; the event
`device-registered`; features `csipAus` and `defaultControl`. No `call`
operations. The capabilities' notes give the upstream evidence for each
limit below.

## Limits

- DERControlBase: `opModConnect`, `opModEnergize`, `opModFixedW` and the
  CSIP-AUS limits `opModImpLimW`, `opModExpLimW`, `opModGenLimW` and
  `opModLoadLimW`. Envoy serves a limit as W / 10^0, with a larger
  multiplier only on an int16 overflow; a limit it would not serve exactly
  is exit 5. `opModMaxLimW`, curve modes and DERCurves are exit 5, so the
  `csip` fixture is.
- Default controls hold the CSIP-AUS limits only, without a description: a
  fixture default with another element is exit 5.
- Envoy describes a control by its start time, not by the fixture's
  description; clients match controls by what they do.
- Envoy serves the registration PIN as five stored digits followed by their
  digit sum modulo 10, while it computes the SFDI check digit so that the
  digit sum is a multiple of 10. A fixture PIN Envoy would serve differently
  is exit 5; the baseline PIN reads the same under both rules.
- `put-der`: Envoy requires `csipaus:doeModesSupported` (namespace
  `https://csipaus.org/ns/v1.3`) in a DERCapability, as CSIP-AUS makes it
  mandatory; the fixtures carry it, and a DERCapability without it is 400.
- `poll-pricing`: tariffs are seeded and served, but Envoy
  puts its CSIP-AUS extension `RateComponentLink` in the IEEE 2030.5
  namespace of each TimeTariffInterval, which a schema-checking client
  (py20305) refuses: a known issue (tests/known-bugs.tsv), so the `pricing`
  feature is not declared. Envoy serves no TimeTariffInterval description, and
  knows the currencies 0, 36, 124, 840 and 978 only. Messaging is not
  served: a fixture with `messages` is exit 5.
- `subscribe` is not declared: Envoy lets only aggregator certificates
  subscribe (a device certificate gets 403, and its EndDevice has no
  SubscriptionListLink), and delivers only to the domains of an aggregator,
  which the admin API cannot create. `ENABLE_NOTIFICATIONS` stays off.
- No flow reservations: Envoy has no FlowReservation function set.
- `client` and `call` print a result document with `unsupported` and exit 5.
