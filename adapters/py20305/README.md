# py20305 adapter

Wraps [py20305](https://github.com/DERSecurity/py20305) (DER Security,
Project Satori), an async Python IEEE 2030.5 / CSIP client, pinned to v0.16.0
(`79844938…`) under Apache-2.0. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The pins are in the
[Dockerfile](Dockerfile) and [versions.yaml](../../versions.yaml).

Client only: `server` validates its flags and exits 5.

## Build

```bash
make images ADAPTERS=py20305
make smoke-py20305              # needs the gridappsd image as the server
```

The image installs py20305 from the pinned source, and every dependency from
[requirements.txt](requirements.txt): a lock with the hash of every wheel,
for linux/amd64 and linux/arm64. py20305 has no lock of its own.
`make lock-py20305` regenerates it from [requirements.in](requirements.in)
with a pinned uv.

## How the adapter works

[adapter/ieee20305_interop.py](adapter/ieee20305_interop.py) runs each
`client <operation>` on the py20305 engine:

| Operation | py20305 |
|-----------|---------|
| `discover` | `Sep2Client.get` of the DeviceCapability |
| `register` | `CsipClient.connect` (its discovery); `CsipClient.register_end_device` when the server does not list the device; then a GET of the Registration, whose PIN must be the fixture's |
| `poll-controls` | `CsipClient.connect`; the DER programs, default controls and controls it discovered must include the fixture's |
| `report` | a `MirrorUsagePoint` built with py20305's models, POSTed with `Sep2Client.post` |

Before each operation the adapter runs a TLS handshake with py20305's own
SSL context, so that a refused certificate is `connect-failed` (exit 3).
py20305's retries are switched off, so that a failure shows at once.
`--version 2018` sets py20305's `server_2018_compat`.

`IEEE20305_INTEROP_LOG_LEVEL=DEBUG` shows py20305's log, with every request,
on stderr.

## Limits

- TLS 1.2 with `TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384` and
  `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8`. It meets the GridAPPS-D server on
  CCM_8.
- py20305 verifies the server's host name, so the server certificate must
  name the host in `--url`. (The GridAPPS-D client checks only the chain.)
- `subscribe`, `client run` and `call` are not implemented (exit 5).
