# Container contract

Version: **0.1 (draft)**. `contract_version` in [versions.yaml](../versions.yaml).

> Not yet implemented by any adapter. All four adapters (`epri`, `envoy`,
> `py20305`, `gridappsd`) are at status `planned`. It stays a draft (0.x)
> until at least one adapter implements every part of it;
> [Gaps before 1.0](#gaps-before-10) lists what is missing. Until 1.0 it may
> change without a major version bump.

Every adapter image exposes the same commands, flags, JSON documents and exit
codes. Consumers depend on this document, not on any adapter's source. After
1.0, a breaking change bumps the major version and an addition bumps the
minor version. Documents carry `schemaVersion` `"1.0"`, which names the
compatible line and changes only with the major version.

## Contents

- [Image layout](#image-layout)
- [How IEEE 2030.5 shapes this contract](#how-ieee-20305-shapes-this-contract)
- [Commands](#commands)
- [`server`](#server)
- [`client`](#client)
- [`call`](#call)
- [Answers](#answers)
- [Transport security](#transport-security)
- [Events](#events)
- [Result document](#result-document)
- [`print-capabilities`](#print-capabilities)
- [`print-fixture`](#print-fixture)
- [Exit codes](#exit-codes)
- [Output rules](#output-rules)
- [Gaps before 1.0](#gaps-before-10)
- [Changes](#changes)

## Image layout

| Item | Value |
|------|-------|
| Image | `ghcr.io/otfabric/ieee20305-interop-<adapter>` |
| Entrypoint | `ieee20305-interop` (`/usr/local/bin/ieee20305-interop`) |
| Default command | `server` |
| Protocol | IEEE 2030.5 (SEP 2.0) over HTTPS with mutual TLS |
| Port | 443/tcp |
| Path | Resource-based: `/dcap` (DeviceCapability), `/edev` (EndDeviceList), `/edev/{id}/derp` (DERProgramList), etc. |
| Security | Mutual TLS with device certificates; device identity by LFDI (Long-Form Device Identifier, SHA-256 of the certificate) and SFDI (Short-Form Device Identifier, truncated fingerprint) |
| User | non-root, uid 10001 |
| Fixtures | `/fixtures/<name>/fixture.json`, baked in; mount over `/fixtures` to replace, or pass `--fixture` |
| Readiness file | `/run/ieee20305-interop/ready` |
| Control socket | `/run/ieee20305-interop/control.sock` (see [`call`](#call)) |
| Health check | built in: `test -f /run/ieee20305-interop/ready` |
| Licences | `/licenses/` |

## How IEEE 2030.5 shapes this contract

The contracts of the sibling repositories (OCPP, Modbus, IEC 104) have a
server with a one-shot or long-running client. IEEE 2030.5 has its own shape:

1. **HTTPS, not WebSocket.** IEEE 2030.5 uses RESTful HTTPS. Clients poll
   for resources; servers may also push via long-poll or subscription
   notifications. Every request is an HTTP method on a resource URL.
2. **Mutual TLS is mandatory.** Unlike OCPP's optional security profiles,
   IEEE 2030.5 requires mTLS in every deployment. Device identity is derived
   from the TLS certificate (LFDI/SFDI). The test generates certificates and
   mounts them.
3. **XML on the wire, JSON in the contract.** The protocol uses XML
   (application/sep+xml). The adapter translates to and from JSON for the
   contract's documents (events, results, fixtures). This keeps the contract
   language-neutral.
4. **Resource-oriented operations.** Instead of named RPC actions, IEEE
   2030.5 has resources (EndDevice, DERProgram, DERControl, MeterReading,
   etc.). The contract names operations by what they do: `register`,
   `discover`, `poll-controls`, `report`, `subscribe`.
5. **Answers come from the fixture.** When a server receives a request, it
   answers with the response the fixture defines for that resource and
   operation.

## Commands

```text
ieee20305-interop server [flags]
ieee20305-interop client run --url URL [flags]
ieee20305-interop client <operation> --url URL [flags]
ieee20305-interop call <operation> [--device SFDI] [--payload JSON] [--timeout-ms N] [--control-socket PATH]
ieee20305-interop print-capabilities
ieee20305-interop print-fixture [NAME]
ieee20305-interop version
```

Flags have the form `--name value`. An unknown flag, a flag given twice, a
missing required flag, or a value that is not valid for the flag is a usage
error (exit 2). Flags may appear in any order.

### Common flags

| Flag | Commands | Description |
|------|----------|-------------|
| `--port N` | `server` | Listen port (default 443) |
| `--tls-dir DIR` | `server`, `client` | Directory with the TLS material (see [Transport security](#transport-security)) |
| `--fixture PATH` | `server`, `client run` | Path to a fixture file; default: the baked baseline |
| `--ready-file PATH` | `server`, `client run` | Path to write when ready (default `/run/ieee20305-interop/ready`) |
| `--control-socket PATH` | `server`, `client run` | Unix socket for `call` (default `/run/ieee20305-interop/control.sock`) |
| `--connect-timeout-ms N` | `client` | Connection timeout in milliseconds |
| `--timeout-ms N` | `call` | Per-operation timeout in milliseconds |
| `--url URL` | `client` | Server URL (`https://host:port`) |
| `--device SFDI` | `call` (server) | Target device for server-initiated operations |
| `--payload JSON` | `client <op>`, `call` | JSON payload for the operation |
| `--hold-ms N` | `client run` | Disconnect after N ms (for timed tests) |

## `server`

Starts an IEEE 2030.5 server (utility/aggregator role). It listens on the
configured port with mTLS, serves the resources the fixture defines, and
answers requests from registered EndDevices.

The server creates the readiness file once it is listening and accepting TLS
connections. Events are written to stdout as JSON Lines.

## `client`

### `client run`

Connects to a server, registers the fixture's EndDevice, and stays
connected. Use `docker exec <container> ieee20305-interop call ...` to make
it perform operations on the open connection.

### `client <operation>`

A one-shot client: connect, perform one operation, print the result document,
disconnect. Operations:

| Operation | Description |
|-----------|-------------|
| `register` | POST the EndDevice to `/edev` |
| `discover` | GET `/dcap` and follow links to discover resources |
| `poll-controls` | GET the DERControlList for the device's DER program |
| `report` | POST a MeterReading or DERStatus to the server |
| `subscribe` | POST a Subscription for a resource |

## `call`

Sends an operation through the control socket of a running `server` or
`client run` container:

```bash
docker exec <container> ieee20305-interop call register
docker exec <container> ieee20305-interop call --device <SFDI> poll-controls
```

From a server, `--device SFDI` names the target EndDevice. From a client
run, the device is the fixture's own EndDevice.

## Answers

When a server receives a request, it looks up the answer in the fixture's
`answers.server` for the operation and responds with the fixture's payload
(translated to XML). When a client run receives a server-initiated request
(e.g., a DERControl update via subscription), it looks up
`answers.client`.

An operation without an answer in the fixture returns HTTP 404
(NotFound) or the IEEE 2030.5 equivalent error response.

## Transport security

IEEE 2030.5 mandates mutual TLS. The `--tls-dir` directory contains:

```text
server/     server.crt, server.key, ca.crt
client/     client.crt, client.key, ca.crt
```

- `server.crt` / `server.key`: the server's TLS certificate and key.
- `client.crt` / `client.key`: the client's device certificate and key.
  The LFDI is the SHA-256 fingerprint of `client.crt`; the SFDI is the
  first 36 bits of the LFDI, encoded as a decimal with a check digit.
- `ca.crt`: the Certificate Authority that signed the peer's certificate.

The test scripts generate all TLS material at runtime; nothing is committed
or baked into images.

## Events

Both `server` and `client run` write JSON Lines to stdout. Every line is a
JSON object with at least `event` and `time` fields.

### Server events

| Event | Fields | When |
|-------|--------|------|
| `ready` | `adapter`, `port`, `fixture` | Listening and accepting connections |
| `device-registered` | `sfdi`, `lfdi` | An EndDevice registered |
| `request-received` | `sfdi`, `method`, `path`, `operation` | Any request received |
| `response-sent` | `sfdi`, `method`, `path`, `status` | Response sent |
| `subscription-created` | `sfdi`, `resource` | A subscription was created |
| `connection-rejected` | `sfdi`, `reason` | TLS handshake or identity rejected |
| `stopped` | | Clean shutdown |

### Client events

| Event | Fields | When |
|-------|--------|------|
| `ready` | `sfdi`, `url` | Connected and registered |
| `request-sent` | `method`, `path`, `operation` | A request was sent |
| `response-received` | `method`, `path`, `status` | Response received |
| `control-received` | `operation`, `payload` | A control update received |
| `connection-failed` | `detail` | Cannot connect or TLS failure |
| `stopped` | | Clean shutdown |

## Result document

A one-shot `client <operation>` prints one JSON line to stdout: the result
document.

```json
{
  "schemaVersion": "1.0",
  "adapter": "epri",
  "version": "2018",
  "operation": "discover",
  "ok": true,
  "error": null,
  "request": {"method": "GET", "path": "/dcap"},
  "response": {"status": 200, "body": {}},
  "elapsedMs": 42
}
```

On failure:

```json
{
  "schemaVersion": "1.0",
  "adapter": "epri",
  "version": "2018",
  "operation": "register",
  "ok": false,
  "error": {"code": "connect-failed", "detail": "TLS handshake failed"},
  "request": null,
  "response": null,
  "elapsedMs": 1502
}
```

Error codes: `connect-failed`, `timeout`, `not-connected`, `unsupported`,
`invalid-request`, `server-error`.

## `print-capabilities`

Prints one JSON line: the adapter's capability document, which validates
against [schemas/capabilities.schema.json](../schemas/capabilities.schema.json).
See [docs/CAPABILITIES.md](CAPABILITIES.md).

## `print-fixture`

```bash
ieee20305-interop print-fixture [NAME]
```

Prints the named baked fixture (default `baseline`) as JSON to stdout. The
fixture validates against the fixture schema. See [docs/FIXTURES.md](FIXTURES.md).

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Operation failed (server error, timeout, not-connected) |
| 2 | Usage error (bad flags, missing required arguments) |
| 3 | Connection failed (TLS handshake, DNS, network) |
| 4 | Invalid fixture (parse error, schema violation, bad certificate reference) |
| 5 | Unsupported (operation or feature the adapter does not implement) |

## Output rules

1. **stdout is structured.** `server` and `client run` write JSON Lines
   (one JSON object per line). `client <op>` writes one JSON line (the
   result document). `print-capabilities` and `print-fixture` each write
   one JSON document. `version` writes one line.
2. **stderr is for humans.** Diagnostic messages, progress, errors go to
   stderr.
3. **No mixing.** A consumer reads stdout with `jq`; anything that is not
   JSON on stdout is a contract violation.

## Gaps before 1.0

- [ ] No adapter is built yet (all `planned`).
- [ ] Subscription/notification push model not fully specified.
- [ ] Server-initiated DERControl dispatch through `call` not implemented.
- [ ] CSIP and CSIP-AUS profile-specific behaviours not tested.
- [ ] Flow reservation (IEEE 2030.5 pricing/billing) not covered.
- [ ] Device capability negotiation details to be refined after first adapter.

## Changes

### 0.1 (2026-10-22)

- Initial draft, adapted from ocpp-interop contract v0.5.
- IEEE 2030.5 resource model, mTLS, LFDI/SFDI identity.
- Operations: register, discover, poll-controls, report, subscribe.
