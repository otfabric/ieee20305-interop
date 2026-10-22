# Capabilities

`ieee20305-interop print-capabilities` prints one JSON line, which validates
against [schemas/capabilities.schema.json](../schemas/capabilities.schema.json).
A consumer reads it to decide which scenarios to run against an image, and
reports the rest as `SKIP_UNSUPPORTED`.

## Rules

1. A capability is `true`, or an operation is listed, only when the self-tests
   of this repository exercise it (REQUIREMENTS.md R4.2). An upstream's
   documentation alone is not enough.
2. Capabilities are declared per IEEE 2030.5 version and per role. Support
   for a feature in the 2018 edition says nothing about 2023.
3. `operations` lists the operations the adapter can perform in that role.
   An operation missing from the list is unsupported.
4. `tls` lists what the adapter offers on the wire, in both roles. Two
   adapters can be paired only when their cipher suites intersect.
5. `fixtures` lists the baked fixtures a role runs with: a server is
   seeded with them, a client sends and checks them. Absent means
   `baseline` only. A test runs a fixture only where both ends declare it.
6. `calls` lists the [`call`](CONTAINER_CONTRACT.md#call) operations of a
   role, and `events` the optional
   [events](CONTAINER_CONTRACT.md#events) it writes.
7. `notes` explains limitations in plain words, with a link to evidence
   where there is one.

## Structure

```json
{
  "schemaVersion": "1.0",
  "adapter": "gridappsd",
  "adapterVersion": "dev",
  "contractVersion": "0.3",
  "upstream": {
    "name": "GridAPPS-D IEEE 2030.5 server and client",
    "repository": "https://github.com/GRIDAPPSD/ieee-2030_5-server-go",
    "version": "v0.18.0",
    "revision": "d7a4a17042af7506610a973c6be8a5466db34053",
    "license": "LicenseRef-Battelle-BSD",
    "language": "Go",
    "independentEngine": true,
    "client": {
      "repository": "https://github.com/GRIDAPPSD/ieee-2030_5-client-go",
      "version": "v0.1.0",
      "revision": "66570b94ea104b46eb12b48ef2451b3454050176"
    }
  },
  "tls": {
    "versions": ["1.2"],
    "cipherSuites": ["TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"]
  },
  "versions": {
    "2018": {
      "client": {"supported": true, "fixtures": ["baseline", "csip"],
                 "operations": ["register", "discover", "poll-controls", "report", "respond", "log-event", "put-der", "flow-reservation"]},
      "server": {"supported": true, "fixtures": ["baseline", "csip"], "calls": ["issue-control"],
                 "events": ["device-registered", "subscription-created"],
                 "operations": ["register", "discover", "poll-controls", "report", "subscribe", "respond", "log-event", "flow-reservation"]},
      "features": {"mTLS": true, "csip": true, "csipAus": false, "subscription": true, "defaultControl": true,
                   "curves": false, "pricing": false, "messaging": false, "flowReservation": true},
      "notes": ["Client: subscribe is not declared: ..."]
    }
  }
}
```

## Upstream

`upstream` names the implementation the adapter wraps, pinned to a release
and its commit. When the client role comes from a different repository,
`upstream.client` pins that one too.

## TLS

| Field | Description |
|-------|-------------|
| `versions` | TLS versions offered, such as `"1.2"` |
| `cipherSuites` | IANA names of the cipher suites offered, such as `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8` (the one IEEE 2030.5 mandates) and `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256` |

## Versions

| Key | Standard |
|-----|----------|
| `2018` | IEEE 2030.5-2018 |
| `2023` | IEEE 2030.5-2023 |

## Roles

| Role | Description |
|------|-------------|
| `client` | A device (EndDevice / DER client) that connects to a server |
| `server` | A utility or aggregator server that manages devices |

## Features

| Feature | Description |
|---------|-------------|
| `mTLS` | Mutual TLS with device certificates (LFDI/SFDI) |
| `csip` | Common Smart Inverter Profile (CSIP) |
| `csipAus` | CSIP-AUS (Australian) profile |
| `subscription` | IEEE 2030.5 Subscription/Notification |
| `defaultControl` | DefaultDERControl (optional key; absent means false) |
| `curves` | DERCurve (optional key) |
| `pricing` | TariffProfile, RateComponent, TimeTariffInterval (optional key) |
| `messaging` | MessagingProgram, TextMessage (optional key) |
| `flowReservation` | FlowReservationRequest (optional key) |

## Operations

What each operation does is defined in
[the contract](CONTAINER_CONTRACT.md#client-operation): `register`,
`discover`, `poll-controls`, `report`, `subscribe`, `respond`,
`log-event`, `put-der`, `flow-reservation`, `poll-pricing`. A server
declares the operations it serves to a client.

## Declared now

Contract 0.3, upstream pins of [versions.yaml](../versions.yaml). Known
upstream defects that make a declared operation fail against a peer are in
[tests/known-bugs.tsv](../tests/known-bugs.tsv).

| Adapter | Role | Versions | Operations | Fixtures | Calls, events | Cipher suites |
|---------|------|----------|------------|----------|---------------|---------------|
| `gridappsd` | server | 2018, 2023 | register, discover, poll-controls, report, subscribe, respond, log-event, flow-reservation | baseline, csip | `issue-control`; `device-registered`, `subscription-created` | CCM_8 |
| `gridappsd` | client | 2018, 2023 | register, discover, poll-controls, report, respond, log-event, put-der, flow-reservation | baseline, csip | | CCM_8 |
| `envoy` | server | 2018 | register, discover, poll-controls, report, respond, log-event, put-der, poll-pricing | baseline, csip-aus | `device-registered` | CCM_8, AES-128-GCM, AES-256-GCM |
| `py20305` | client | 2018 | register, discover, poll-controls, report, subscribe, respond, log-event, put-der, poll-pricing | baseline, csip, csip-aus | | AES-256-GCM, CCM_8 |
| `py20305` | client | 2023 | register, discover, poll-controls, report, subscribe, respond, log-event | baseline, csip | | AES-256-GCM, CCM_8 |
| `epri` | client | 2018 | register, discover, poll-controls, report, respond | baseline | | CCM_8 |

What is not declared, and why (each adapter's `notes` give the evidence):

- **Subscriptions** work from py20305 to GridAPPS-D only. The GridAPPS-D
  client's notification listener accepts CCM_8 and a client certificate
  only, and sep2server delivers with Go's standard TLS and no client
  certificate. Envoy accepts subscriptions from aggregator certificates only.
  EPRI's client has them commented out.
- **Curves**: sep2server takes curves from its boot fixture only and does not
  serve `/dc/{id}`; Envoy serves none. No baked fixture has curves.
- **Pricing** is seeded and served by Envoy, but no client of the self-tests
  reads it (py20305 refuses Envoy's TimeTariffIntervalList, a known issue;
  the GridAPPS-D client has no pricing types). **Messaging** cannot be
  seeded into any server adapter.
- **put-der** is served by Envoy only: sep2server's EndDevice has no
  DERListLink.
- **flow-reservation** is served by GridAPPS-D only, and performed by its
  own client only.

See [versions.yaml](../versions.yaml) for the pins and the candidates.
