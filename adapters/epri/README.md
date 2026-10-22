# epri adapter

Wraps the [EPRI IEEE 2030.5 Client](https://github.com/epri-dev/IEEE-2030.5-Client),
a C client library and its test application `client_test`, pinned to v0.2.11
(commit `505ee47d…`, July 2018; EPRI tags it but publishes no release) under
BSD-3-Clause. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The pins
are in the [Dockerfile](Dockerfile) and [versions.yaml](../../versions.yaml).

Client only, run against IEEE 2030.5-2018 servers; its schema is EPRI's
`sep.xsd` 2.0.4. `server` validates its flags and exits 5.

## Build

```bash
make images ADAPTERS=epri
make smoke-epri              # needs the gridappsd image as the server
```

`client_test` is built from the pinned source with EPRI's own `build.sh`
and Debian trixie's gcc and OpenSSL 3, with two changes:

- `targets.sh` refuses any OpenSSL but 1.1.0; the build accepts OpenSSL 3.
- [patches/0001-sha256-padding.patch](patches/0001-sha256-padding.patch):
  EPRI's own SHA-256 computes a negative padding for messages whose length
  modulo 64 is 56 to 63, and crashes. It hashes the device certificate for
  the LFDI, so it crashed at start-up for one generated certificate in eight,
  which would make every result depend on the test's random certificate.

[openssl.cnf](openssl.cnf) sets OpenSSL's security level to 0: the client
sets the cipher list `ECDHE-ECDSA-AES128-CCM8` and relies on the default
level, under which OpenSSL 1.1.0 offers CCM_8 and OpenSSL 3 does not.

## How the adapter works

[adapter/ieee20305_interop.py](adapter/ieee20305_interop.py) (Python standard
library, Debian's python3) runs `client_test` once per `client <operation>`:

| Operation | client_test | Complete when |
|-----------|-------------|---------------|
| `discover` | retrieves the URL | it printed the DeviceCapability |
| `register` | `inverter register` | it printed `registration succeeded` |
| `poll-controls` | `inverter all` | it printed the DERControlList of every program; then the fixture's programs and controls must be among them |
| `report` | `inverter fsa meter` | its `POST` of a MirrorUsagePoint to the MirrorUsagePointList got 201 with a `Location` |
| `respond` | `inverter all` | a DERControlResponse it posted to the `replyTo` of the fixture's control active now (start 0) got 201 or 204 |

Every operation the adapter performs is declared; the failures that upstream
defects cause are in [tests/known-bugs.tsv](../../tests/known-bugs.tsv).
`report` posts `client_test`'s own MirrorUsagePoint, not the fixture's, and
`respond` sends the statuses `client_test` chooses (listed in
`response.body`). `subscribe`, `log-event`, `put-der`, `flow-reservation`,
`poll-pricing`, `client run` and `call` are `unsupported` (exit 5); the
flags of contract 0.3 (`--notify-listen`, `--notify-host`; `call --device
--payload --timeout-ms --control-socket`) are validated first. The client
declares the `baseline` fixture: only the baseline is exercised against a
server it can talk to (GridAPPS-D).

`client_test` prints its HTTP exchanges and every document it parsed, runs
until stopped, and exits 0 whatever happens. The adapter relays the output to
stderr, reads the documents from it, decides when the operation is complete,
and stops the client. It converts the device certificate to DER (the client
derives the LFDI from the certificate file), resolves the URL's host to an IP
address (the client takes IP literals), and runs the client from
`/opt/epri`, where `register` finds EPRI's device settings.

## Limits

Line numbers are of the pinned commit `505ee47`.

- `register` checks for the PIN 111115, hard-coded in `client_test`
  (`client_test.c:327`); a fixture with another PIN is `invalid-request`.
- Its parser follows the draft `sep.xsd` 2.0.4 strictly: against Envoy it
  rejects the CSIP-AUS element `csipaus:ConnectionPointLink` in EndDevice
  (`parse.c:215`), so `register` fails (known upstream issue,
  [tests/known-bugs.tsv](../../tests/known-bugs.tsv)) and nothing past the
  EndDeviceList works.
- Everything it sends is in the draft name space `http://ieee.org/2030.5`
  (`se_schema.c:2235`), not IEEE 2030.5-2018's `urn:ieee:std:2030.5:ns`.
  GridAPPS-D refuses such a body with 400 (seen for the MirrorUsagePoint of
  `fsa meter`).
- `poll-controls` crashes against GridAPPS-D: `get_seq()` formats the
  DERControlList href and its query into `char uri[64]` (`retrieve.c:140`),
  and GridAPPS-D's hrefs are longer. Against Envoy, the EndDevice does not
  parse. A fixture with default controls or curves is `unsupported`, as the adapter matches
  programs and controls only. `client_test` prints a present optional
  boolean as `true` whatever its value (its output code reads the presence
  bit), so booleans match by presence only.
- `report`: the meter test (`client_test.c:250-265`, `metering.c`) posts a
  MirrorUsagePoint of its own (random mRID, roleFlags 09, seven readings
  with hard-coded values), not the fixture's, and only once it has read the
  EndDevice's FunctionSetAssignments and DER programs (`meter` alone never
  completes the EndDevice, `metering` reads the DER controls and crashes);
  GridAPPS-D refuses it (name space, 400), and Envoy's EndDevice does not
  parse.
- `respond`: `client_test` posts DERControlResponses (`schedule.c:142-151`)
  only in its schedule test (`all`): Received (1) when `responseRequired`
  has bit 0, Started (2) and the others when it has bit 1. It crashes on
  GridAPPS-D before it reads a control, and its responses are in the draft
  name space.
- `log-event`: the alarm test posts its own LogEvent ("Alarms Test", PEN
  37244, no functionSet; `client_test.c:188-208`) after reading the DER
  controls, so it crashes on GridAPPS-D as `poll-controls` does.
- `put-der`: `client_test` PUTs the DER resources of its own `./settings`
  files (draft 2.0.4 elements), not the fixture's, to the first DER of the
  EndDevice's DERList (`client_test.c:214-231`); GridAPPS-D's EndDevice has
  no DERListLink.
- `subscribe`: the subscription code is commented out
  (`client_test.c:235-239`; `der_client.c:14` leaves out `subscribe.c`). `flow-reservation` and
  `poll-pricing` are not in `client_test`.
- TLS 1.2 with CCM_8 only; it does not check the server's host name.
