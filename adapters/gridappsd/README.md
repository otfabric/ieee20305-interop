# gridappsd adapter

Wraps the GridAPPS-D IEEE 2030.5 implementations, both written in Go:

| Role | Upstream | Pin |
|------|----------|-----|
| server | [ieee-2030_5-server-go](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) (`sep2server`) | v0.18.0, `d7a4a170…` |
| client | [ieee-2030_5-client-go](https://github.com/GRIDAPPSD/ieee-2030_5-client-go) | v0.1.0, `66570b94…` |

Both are under the Battelle BSD licence (`LicenseRef-Battelle-BSD`); see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The pins are in the
[Dockerfile](Dockerfile) and [versions.yaml](../../versions.yaml).

## Build and run

```bash
make images ADAPTERS=gridappsd
make pki
make run-gridappsd
```

`make run-gridappsd` serves IEEE 2030.5-2018 on `https://127.0.0.1:8443/dcap`
with the material of `./tls/server`; `docker compose up -d --wait` does the
same on port 8444.

## How the adapter works

The adapter ([adapter/](adapter/)) is compiled inside the client module,
because it drives the client through that module's internal packages.

**Server.** `server` starts `sep2server serve` with:

- the certificate directory of `--tls-dir`;
- a throwaway serving CA, so that the admin API can start; devices are
  still verified with `--tls-dir/ca.crt`;
- the admin API on a loopback port with a random key, and no data directory
  (state lives in memory);
- its traffic capture on (`SEP2_TRAFFIC_CAPTURE`), which is how the adapter
  sees registrations and subscriptions: sep2server logs neither. The adapter
  reads the admin stream `GET /api/traffic/stream` and writes
  `device-registered` for each `POST /edev` answered 201, and
  `subscription-created` for each `POST /edev/{id}/sub` answered 201 (the
  `resource` is the `subscribedResource` of the captured request);
- `SSL_CERT_FILE` set to `--tls-dir/ca.crt`: sep2server delivers
  notifications with Go's standard client, which verifies the listener
  against the system roots.

When the server is up, the adapter provisions each certificate of
`--tls-dir/devices` through the admin API: the device by LFDI and SFDI with
the fixture's PIN, one FunctionSetAssignments (`POST /api/fsas`, assigned to
the device), and under it each of the fixture's programs with its
description, primacy, mRID and default control, then its controls
(`POST /api/der/controls`). Then it opens the control socket and writes
`ready`. The upstream's own logs go to stderr.

`call issue-control` (in the container) sends the control of `--payload`
through the control socket; the server issues it through the admin API to
the named program of each device (or of `--device`), and sep2server
notifies the subscribers of the program's lists.

**Patch.** [patches/0001](patches/0001-EndDevice-advertise-the-SubscriptionListLink.patch)
makes sep2server advertise the `SubscriptionListLink` of each EndDevice: v0.18.0
mounts `GET`/`POST /edev/{id}/sub` but never links to it. The Dockerfile
applies every patch of `patches/` after the pin check.

**Client.** Each `client <operation>` makes one `SEP2Client` with the
material of `--tls-dir` and runs the operation (see
[the contract](../../docs/CONTAINER_CONTRACT.md#client-operation));
`flow-reservation` uses a second client of the upstream's aggregator role,
the only role allowed to post flow reservations. `subscribe` runs the
upstream `NotifyReceiver` with `notify.crt` (or the device certificate).

## Limits

- TLS 1.2 with `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8` only, in both roles,
  through the GridAPPS-D fork of `crypto/tls`. A peer that does not offer
  CCM_8, such as one built on Go's standard `crypto/tls`, cannot connect.
- The server seeds the control functions `opModConnect`, `opModEnergize`,
  `opModMaxLimW` and `opModFixedW`, durations of 60 s to 24 h and starts up
  to 7 days ahead, with several programs and their default controls
  (fixtures `baseline` and `csip`). Curves, CSIP-AUS limits (`csip-aus`),
  tariffs and messages are exit 5: sep2server serves curves only from its boot fixture
  and does not serve `GET /dc/{id}`, and has no CSIP-AUS functions, pricing
  or messaging seeding.
- The server picks the hrefs of programs and the mRIDs of controls; a
  DefaultDERControl carries no description. It serves no DERList, so
  `put-der` is not a server operation.
- Client `subscribe` is not declared: the upstream listener accepts only
  CCM_8 and a client certificate, and sep2server delivers with neither.
- Client `poll-pricing` is exit 5 (no pricing types upstream), and fixtures
  with CSIP-AUS limits are exit 5 for `poll-controls` and `respond`.
- `put-der` sends DERCapability and DERSettings as adapter types through the
  upstream client: `sep2.DERSettings` lacks `setGradW`, and the core types
  lack the CSIP-AUS `doeModesSupported`/`doeModesEnabled`.
- `client run` is not implemented (exit 5).
