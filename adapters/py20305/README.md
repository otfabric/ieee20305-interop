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
| `poll-controls` | `CsipClient.connect`; the DER programs, default controls, controls and curves it discovered must include the fixture's (programs by description, controls by DERControlBase and duration, default controls by DERControlBase, curves by curveType, multipliers, yRefType and points; CSIP-AUS limits through `csipaus.elements.doe_limits`, compared as powers). A curve a control links to that the DERCurveList does not hold is fetched with `Sep2Client.get` |
| `report` | a `MirrorUsagePoint` built with py20305's models, POSTed with `Sep2Client.post` |
| `subscribe` | py20305's `NotificationServer` on `--notify-listen`, with `notify.crt` and `notify.key` (the device certificate without them) and `client_cert_mode` `warn`; a `Subscription` model (XML, `+S1`, limit 1) of the first FunctionSetAssignments' DERProgramList, POSTed with `Sep2Client.post` to the EndDevice's SubscriptionListLink; then it waits for a Notification with status 0 |
| `respond` | a `DercontrolResponse` (status 2, `modesResponded` from `build_modes_responded` for 2023) for the served control that matches the fixture's first control with `start` 0, POSTed to its `replyTo` |
| `log-event` | a `LogEvent` model from the fixture's `logEvent`, POSTed to the EndDevice's LogEventListLink |
| `put-der` | `Dercapability` and `Dersettings` models (with the fixture's CSIP-AUS `doeModesSupported` and `doeModesEnabled` when it has them, in py20305's CSIP-AUS mode), `build_der_status` and `build_der_availability`, PUT with `Sep2Client.put` to the links of the first DER |
| `poll-pricing` | `CsipClient(pricing_enabled=True)` discovery (a fixture without tariffs has nothing to match, and is ok) of the tariffs (the DeviceCapability's TariffProfileListLink when no FunctionSetAssignments has one), and the first ConsumptionTariffInterval of each TimeTariffInterval with `Sep2Client.get_list` |

The adapter POSTs responses, log events and subscriptions with
`Sep2Client.post` itself: py20305's `post_der_response` and
`SubscriptionManager.subscribe` log a refused request instead of raising it.
`Sep2Client.post` returns only the `Location`, so the adapter reads the HTTP
status from a `TrafficRecorder` it installs. py20305's discovery detects a
CSIP-AUS server but does not tell its HTTP client, so the adapter sets
`Sep2Client.csip_aus_mode` itself.

Before each operation the adapter runs a TLS handshake with py20305's own
SSL context, so that a refused certificate is `connect-failed` (exit 3).
py20305's retries are switched off, so that a failure shows at once.
`--version 2018` sets py20305's `server_2018_compat`.

`IEEE20305_INTEROP_LOG_LEVEL=DEBUG` shows py20305's log, with every request,
on stderr.

## Limits

- TLS 1.2 with `TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384` and
  `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8`, for the client and the notification
  listener. It meets the GridAPPS-D server on CCM_8; the GridAPPS-D server
  delivers notifications on AES_256_GCM.
- py20305 verifies the server's host name, so the server certificate must
  name the host in `--url`. (The GridAPPS-D client checks only the chain.)
- `flow-reservation` is unsupported (exit 5): py20305 has no flow reservation
  client logic. `client run` and `call` are not implemented (exit 5).
- `put-der` and `poll-pricing` are declared for 2018 and fail against Envoy,
  as recorded in [tests/known-bugs.tsv](../../tests/known-bugs.tsv). py20305
  writes the CSIP-AUS extensions in the namespace `https://csipaus.org/ns`,
  but Envoy expects `https://csipaus.org/ns/v1.3` and refuses the
  DERCapability PUT; with the v1.3 namespace Envoy accepts all four PUTs.
  Envoy's TimeTariffIntervals carry a `RateComponentLink` that no schema
  defines, and py20305's parser, which has no lenient mode, rejects them.
  GridAPPS-D serves neither a DERList nor tariffs.
- Curves are checked, but no server of the self-tests serves them, so no
  baked fixture has them and the `curves` feature is not declared.
