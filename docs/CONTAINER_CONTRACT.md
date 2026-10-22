# Container contract

Version: **0.2 (draft)**. `contract_version` in [versions.yaml](../versions.yaml).

> Implemented by the `gridappsd` (server and client), `envoy` (server),
> `py20305` (client) and `epri` (client) adapters, without `call`,
> `client run` and `subscribe`. It stays a draft (0.x) until the adapters
> together implement every part of it;
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
| Protocol | IEEE 2030.5 over HTTPS with mutual TLS, TLS 1.2 |
| Port | 443/tcp |
| Entry point of the resource tree | `/dcap` (DeviceCapability); every other path is the server's choice and found through links |
| User | non-root, uid 10001 |
| Fixtures | `/fixtures/<name>/fixture.json`, baked in; mount over `/fixtures` to replace, or pass `--fixture` |
| TLS material | none in the image; mounted, see [Transport security](#transport-security) |
| Readiness file | `/run/ieee20305-interop/ready` |
| Control socket | `/run/ieee20305-interop/control.sock` (see [`call`](#call)) |
| Health check | built in: `test -f /run/ieee20305-interop/ready` |
| Licences | `/licenses/`, with `THIRD_PARTY_NOTICES.md` |

## How IEEE 2030.5 shapes this contract

The contracts of the sibling repositories (OCPP, Modbus, IEC 104) have a
server that answers each request from a fixture. IEEE 2030.5 has its own
shape:

1. **HTTPS, not WebSocket.** IEEE 2030.5 is RESTful. Clients poll resources
   and follow links from `/dcap`; servers push only through subscriptions.
2. **Mutual TLS is mandatory.** Device identity is the LFDI and SFDI of the
   device certificate. The tests generate the certificates and mount them;
   there is no plain-HTTP mode.
3. **A server is seeded, not scripted.** A server keeps the state of a
   deployment: provisioned devices, registrations, programs, controls and
   mirrors. The fixture seeds that state when the server starts; the server
   then answers every request itself. A fixture never prescribes an answer
   to a request.
4. **The server picks its paths and names.** Links, `href`s, mRIDs and
   even control descriptions below `/dcap` belong to the server. Clients
   find resources by following links, a fixture's DER programs by their
   description, and its controls by what they do (DERControlBase and
   duration).
5. **XML on the wire, JSON in the contract.** The protocol uses
   `application/sep+xml`. The contract's documents (events, results,
   fixtures, capabilities) are JSON, so that a consumer in any language can
   read them with `jq`.
6. **Operations name what a device does**: `register`, `discover`,
   `poll-controls`, `report`, `subscribe`.

## Commands

```text
ieee20305-interop server --version V --tls-dir DIR [flags]
ieee20305-interop client <operation> --version V --url URL --tls-dir DIR [flags]
ieee20305-interop client run --version V --url URL --tls-dir DIR [flags]
ieee20305-interop call <operation> [--device SFDI] [--payload JSON] [--timeout-ms N] [--control-socket PATH]
ieee20305-interop print-capabilities
ieee20305-interop print-fixture [NAME]
ieee20305-interop version
```

Flags have the form `--name value`. An unknown flag, a flag given twice, a
missing required flag, or a value that is not valid for the flag is a usage
error (exit 2), detected before anything touches the network. Flags may
appear in any order.

### Flags

| Flag | Commands | Description |
|------|----------|-------------|
| `--version V` | `server`, `client` | IEEE 2030.5 edition: `2018` or `2023`; required. One the adapter does not declare is exit 5 |
| `--tls-dir DIR` | `server`, `client` | Directory with the TLS material of this end; required (see [Transport security](#transport-security)) |
| `--url URL` | `client` | URL of the server's DeviceCapability, `https://host[:port]/dcap`; required |
| `--port N` | `server` | Listen port (default 443) |
| `--fixture PATH` | `server`, `client` | Path to a fixture file; default: the baked `baseline` |
| `--ready-file PATH` | `server`, `client run` | Path to create when ready (default `/run/ieee20305-interop/ready`) |
| `--control-socket PATH` | `server`, `client run`, `call` | Unix socket for `call` (default `/run/ieee20305-interop/control.sock`) |
| `--connect-timeout-ms N` | `client` | TCP connection timeout (default 10000) |
| `--timeout-ms N` | `client`, `call` | Timeout of the whole operation (default 30000) |
| `--payload JSON` | `client <op>`, `call` | Operation-specific input; reserved |
| `--device SFDI` | `call` on a server | Target device of a server-initiated operation |

## `server`

Starts an IEEE 2030.5 server (utility or aggregator role) on `--port` with
mutual TLS, for the edition `--version`. Before it is ready, the server:

1. provisions every device whose certificate is in `--tls-dir/devices`, by
   the LFDI and SFDI of that certificate, with the fixture's registration
   PIN. A device that is not provisioned does not get that registration:
   the server refuses it, or lets it register itself with a PIN of its own
   (IEEE 2030.5 in-band registration);
2. seeds, for each provisioned device, the fixture's DER programs with their
   default control and controls (see [docs/FIXTURES.md](FIXTURES.md)).

It then creates the readiness file and writes the `ready` event. On SIGTERM
it stops within 10 seconds, writes `stopped` and exits 0.

A fixture the adapter cannot seed (a control function or a number of
programs the upstream cannot represent) is exit 5, not a silent subset.

## `client`

### `client <operation>`

A one-shot client: connect to `--url`, perform one operation as the device of
`--tls-dir`, print the [result document](#result-document), exit.

| Operation | What the client does | `ok` when |
|-----------|----------------------|-----------|
| `discover` | GET `/dcap` | the server returned a DeviceCapability |
| `register` | find its own EndDevice in the EndDeviceList (by LFDI), POST one if there is none, then GET its Registration | the Registration's PIN is the fixture's |
| `poll-controls` | follow the device's FunctionSetAssignments to its DER programs, and GET their default controls and controls | every DER program of the fixture is served (matched by description), with each of its controls (matched by DERControlBase values and duration) |
| `report` | POST the fixture's MirrorUsagePoint with its MirrorMeterReadings | the server created the mirror (201 with a `Location`) |
| `subscribe` | POST a Subscription to the device's DER program list | the server created the subscription |

### `client run`

Connects, registers, and stays connected as the fixture's device, polling at
the server's poll rates. `docker exec <container> ieee20305-interop call ...`
makes it perform operations. Reserved: no adapter implements it yet (exit 5).

## `call`

Sends an operation through the control socket of a running `server` or
`client run` container. From a server, `--device SFDI` names the target
device. Reserved: no adapter implements it yet (exit 5).

## Transport security

IEEE 2030.5 mandates mutual TLS (TLS 1.2). Each end gets a directory:

```text
server/   server.crt  server.key  ca.crt  devices/<serial>.crt ...
client/   client.crt  client.key  ca.crt
```

- `server.crt`, `server.key`: the server's certificate (ECDSA P-256) and
  key. The certificate has a critical subjectAltName with the names clients
  connect to, a critical keyUsage, and extendedKeyUsage serverAuth and
  clientAuth.
- `client.crt`, `client.key`: the device certificate (ECDSA P-256) and key.
  Its critical subjectAltName holds an RFC 4108 HardwareModuleName (the
  fixture's serial) and it has the IEEE 2030.5 certificate policies.
- `ca.crt`: the CA that signed the other end's certificate.
- `devices/`: the certificates of the devices the server provisions.

Identity: the LFDI is the leftmost 160 bits of the SHA-256 of the device
certificate (DER), as 40 upper-case hex digits. The SFDI is the leftmost 36
bits of it as a decimal, followed by a check digit that makes the sum of all
digits a multiple of 10.

The cipher suite IEEE 2030.5 mandates is
`TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8`. An adapter declares the suites it
offers in its capabilities (`tls.cipherSuites`); a test skips a pairing
whose two ends have no suite in common.

The tests generate all TLS material at run time (`make_pki` in
[scripts/lib.sh](../scripts/lib.sh); `make pki` for local work). Nothing is
committed or baked into an image (REQUIREMENTS.md R7.3).

A `--tls-dir` without the files its command needs is exit 4. A failed TLS
handshake is exit 3 with the error code `connect-failed`.

## Events

`server` and `client run` write JSON Lines to stdout. Every line is a JSON
object with at least `event` and `time` (RFC 3339 UTC).

### Server events

| Event | Fields | When |
|-------|--------|------|
| `device-provisioned` | `sfdi`, `lfdi` | A device of `--tls-dir/devices` was provisioned and seeded |
| `ready` | `adapter`, `version`, `port`, `fixture` | Listening and accepting connections |
| `connection-rejected` | `reason` | A TLS handshake or device identity was refused |
| `stopped` | | Clean shutdown |

Reserved, for adapters whose upstream reports them: `device-registered`
(`sfdi`, `lfdi`), `request-received` (`sfdi`, `method`, `path`),
`subscription-created` (`sfdi`, `resource`).

### Client events (`client run`)

Reserved: `ready` (`sfdi`, `url`), `control-received` (`mRID`,
`description`), `connection-failed` (`detail`), `stopped`.

## Result document

A one-shot `client <operation>` prints one JSON line to stdout, and exits
with the code that matches it:

```json
{
  "schemaVersion": "1.0",
  "adapter": "gridappsd",
  "version": "2018",
  "operation": "report",
  "ok": true,
  "error": null,
  "request": {"method": "POST", "path": "/mup"},
  "response": {"status": 201, "body": {"location": "/mup/7FB5962D656ADCC2FA3BAD4A841833C6"}},
  "elapsedMs": 42
}
```

On failure:

```json
{
  "schemaVersion": "1.0",
  "adapter": "gridappsd",
  "version": "2018",
  "operation": "discover",
  "ok": false,
  "error": {"code": "connect-failed", "detail": "remote error: tls: bad certificate"},
  "request": null,
  "response": null,
  "elapsedMs": 31
}
```

`request` is the last request the operation sent; `response.body` is
informative and adapter-specific (it may be absent). Consumers decide on
`ok`, `error.code` and the exit code.

| `error.code` | Exit | Meaning |
|--------------|------|---------|
| `connect-failed` | 3 | DNS, TCP or TLS handshake failure |
| `timeout` | 1 | `--timeout-ms` elapsed |
| `server-error` | 1 | The server answered with an error, or with something the operation cannot accept |
| `invalid-request` | 1 | The operation could not be built from the fixture or the TLS material |
| `unsupported` | 5 | The adapter does not perform this operation |
| `not-connected` | 1 | Reserved for `call` |

## `print-capabilities`

Prints one JSON line: the adapter's capability document, which validates
against [schemas/capabilities.schema.json](../schemas/capabilities.schema.json).
See [docs/CAPABILITIES.md](CAPABILITIES.md).

## `print-fixture`

```bash
ieee20305-interop print-fixture [NAME]
```

Prints the named baked fixture (default `baseline`), byte for byte as in
this repository. An unknown name is exit 1. See [docs/FIXTURES.md](FIXTURES.md).

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Operation failed (server error, timeout), or an unknown fixture name |
| 2 | Usage error (bad flags, missing required flags), before any network access |
| 3 | Connection failed (DNS, TCP, TLS handshake) |
| 4 | Invalid fixture or TLS material (parse error, schema violation, missing file) |
| 5 | Unsupported (command, operation, edition or fixture content the adapter does not implement) |

## Output rules

1. **stdout is structured.** `server` and `client run` write JSON Lines.
   `client <op>` writes one JSON line (the result document).
   `print-capabilities` writes one JSON line, `print-fixture` one JSON
   document, `version` one line.
2. **stderr is for humans.** Diagnostics, upstream logs and errors go to
   stderr.
3. **No mixing.** Anything on stdout that is not JSON is a contract
   violation, which the smoke and interop tests check.

## Gaps before 1.0

- [ ] `call`, `client run` and the client events: no adapter implements
      them yet.
- [ ] `subscribe` and notifications: specified for the client, not
      implemented; the server side of notifications is not specified.
- [ ] `response.body` is adapter-specific; 1.0 should define a neutral form
      per operation, or drop it.
- [ ] Server events that need upstream hooks (`device-registered`,
      `request-received`) are reserved only.
- [ ] CSIP-AUS behaviour (connection points, site limits) is not
      exercised by any fixture.

## Changes

### 0.2 (2026-10-22)

From implementing the `gridappsd` adapter:

- `--version` and `--tls-dir` are required for `server` and `client`;
  `--url` names the server's `/dcap`. There is no plain-HTTP mode.
- `--tls-dir` layout: one directory per end, with `devices/` for the
  certificates a server provisions. Certificate profile and the LFDI/SFDI
  rules stated.
- Servers are seeded by the fixture (devices, PIN, DER programs, controls)
  instead of answering each request from it. The `answers` and `samples`
  of fixtures are removed.
- Controls are scheduled (`start`) and matched by what they do, not by
  description; an unprovisioned device may register itself with a PIN of
  the server's choosing (from implementing the `envoy` adapter).
- Operations defined by what the client does and when it is `ok`.
- New server event `device-provisioned`; `ready` carries `version`.
- Capabilities declare the TLS versions and cipher suites (`tls`); pairings
  without a common suite are skipped.
- Result-document error codes mapped to exit codes; an unknown fixture name
  in `print-fixture` is exit 1.

### 0.1 (2026-10-22)

- Initial draft, adapted from ocpp-interop contract v0.5.
- IEEE 2030.5 resource model, mTLS, LFDI/SFDI identity.
- Operations: register, discover, poll-controls, report, subscribe.
