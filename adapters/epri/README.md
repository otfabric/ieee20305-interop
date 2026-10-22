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

`client_test` prints its HTTP exchanges and every document it parsed, runs
until stopped, and exits 0 whatever happens. The adapter relays the output to
stderr, reads the documents from it, decides when the operation is complete,
and stops the client. It converts the device certificate to DER (the client
derives the LFDI from the certificate file), resolves the URL's host to an IP
address (the client takes IP literals), and runs the client from
`/opt/epri`, where `register` finds EPRI's device settings.

## Limits

- `register` checks for the PIN 111115, hard-coded in `client_test`; a
  fixture with another PIN is `invalid-request`.
- `client_test` prints a present optional boolean as `true` whatever its
  value: its output code reads the presence bit rather than the value bit
  after it. `poll-controls` therefore matches the fixture's boolean control
  elements by presence only.
- Known upstream issues, in [tests/known-bugs.tsv](../../tests/known-bugs.tsv):
  `poll-controls` against GridAPPS-D overflows a 64-byte URI buffer (its
  hrefs are long), and against Envoy the parser rejects the CSIP-AUS
  element `csipaus:ConnectionPointLink` in EndDevice, so `register` and
  `poll-controls` fail.
- `report` is not offered: `client_test`'s metering test posts a
  MirrorUsagePoint of its own, not the fixture's. Subscriptions are
  commented out in `client_test`. `client run` and `call` exit 5.
- TLS 1.2 with CCM_8 only; it does not check the server's host name.
