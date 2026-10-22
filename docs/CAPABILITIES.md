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
5. `notes` explains limitations in plain words, with a link to evidence
   where there is one.

## Structure

```json
{
  "schemaVersion": "1.0",
  "adapter": "gridappsd",
  "adapterVersion": "dev",
  "contractVersion": "0.2",
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
      "client": {"supported": true, "operations": ["register", "discover", "poll-controls", "report"]},
      "server": {"supported": true, "operations": ["register", "discover", "poll-controls", "report"]},
      "features": {"mTLS": true, "csip": true, "csipAus": false, "subscription": false},
      "notes": ["Subscriptions and the control socket are not implemented by this adapter yet."]
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

## Operations

What each operation does is defined in
[the contract](CONTAINER_CONTRACT.md#client-operation): `register`,
`discover`, `poll-controls`, `report`, `subscribe`. A server declares the
operations it serves to a client.

## Declared now

| Adapter | Status | Roles | Versions | Cipher suites |
|---------|--------|-------|----------|---------------|
| `gridappsd` | implemented | client, server | 2018, 2023 | CCM_8 |
| `envoy` | implemented | server | 2018 | CCM_8, AES-128-GCM, AES-256-GCM |
| `py20305` | implemented | client | 2018, 2023 | AES-256-GCM, CCM_8 |
| `epri` | planned | client | | |

See [versions.yaml](../versions.yaml) for the pins and the candidates.
