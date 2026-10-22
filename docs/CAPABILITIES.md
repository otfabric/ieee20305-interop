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
4. `notes` explains limitations in plain words, with a link to evidence
   where there is one.

## Structure

```json
{
  "schemaVersion": "1.0",
  "adapter": "envoy",
  "adapterVersion": "dev",
  "contractVersion": "0.1",
  "upstream": {
    "name": "ANU Envoy",
    "repository": "https://github.com/bsgip/envoy",
    "version": "0.1.0",
    "revision": "abc123...",
    "license": "MIT",
    "language": "Python",
    "independentEngine": true
  },
  "versions": {
    "2018": {
      "client": {
        "supported": false,
        "operations": []
      },
      "server": {
        "supported": true,
        "operations": ["register", "discover", "poll-controls", "report"]
      },
      "features": {
        "mTLS": true,
        "csip": false,
        "csipAus": true,
        "subscription": false
      }
    }
  }
}
```

## Versions

IEEE 2030.5 has two major editions:

| Key | Standard |
|-----|----------|
| `2018` | IEEE 2030.5-2018 (most widely deployed) |
| `2023` | IEEE 2030.5-2023 (latest revision) |

## Roles

| Role | Description |
|------|-------------|
| `client` | An EndDevice / DER client that connects to a utility server |
| `server` | A utility / aggregator server that manages EndDevices |

## Features

| Feature | Type | Description |
|---------|------|-------------|
| `mTLS` | boolean | Mutual TLS with device certificates (LFDI/SFDI) |
| `csip` | boolean | Common Smart Inverter Profile (CSIP) compliance |
| `csipAus` | boolean | CSIP-AUS (Australian) profile compliance |
| `subscription` | boolean | IEEE 2030.5 Subscription/Notification mechanism |

## Operations

| Operation | From | Description |
|-----------|------|-------------|
| `register` | client | POST EndDevice to the server |
| `discover` | client | GET DeviceCapability and follow resource links |
| `poll-controls` | client | GET DERControlList for active controls |
| `report` | client | POST MeterReading or DERStatus |
| `subscribe` | client | POST Subscription for a resource |

## Declared now

No adapter is built yet. All four are at status `planned` in
[versions.yaml](../versions.yaml).

### Target capabilities

| Adapter | Role | IEEE 2030.5 versions | Profile |
|---------|------|---------------------|---------|
| `epri` | client | 2018 | CSIP |
| `envoy` | server | 2018 | CSIP-AUS |
| `py20305` | client | 2018, 2023 | CSIP |
| `gridappsd` | both | 2018 | CSIP |
