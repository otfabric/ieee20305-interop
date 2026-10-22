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
  (state lives in memory).

When the server is up, the adapter provisions each certificate of
`--tls-dir/devices` through the admin API: the device by LFDI and SFDI with
the fixture's PIN, its default control and controls, and the program's
description and primacy. Then it writes `ready`. The upstream's own logs go to
stderr.

**Client.** Each `client <operation>` makes one `SEP2Client` with the
material of `--tls-dir` and runs the operation (see
[the contract](../../docs/CONTAINER_CONTRACT.md#client-operation)).

## Limits

- TLS 1.2 with `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8` only, in both roles,
  through the GridAPPS-D fork of `crypto/tls`. A peer that does not offer
  CCM_8, such as one built on Go's standard `crypto/tls`, cannot connect.
- The server seeds one DER program per device, with the control functions
  `opModConnect`, `opModEnergize` and `opModMaxLimW`. Anything else in a
  fixture is exit 5.
- The server picks the mRIDs and hrefs of programs and controls itself.
- `subscribe`, `client run` and `call` are not implemented (exit 5).
