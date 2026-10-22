# Fixtures

A fixture describes, for one IEEE 2030.5 deployment scenario:

- the EndDevices a server accepts, and the device identity a client uses;
- the DER programs and controls the server serves;
- how either end answers the requests it receives;
- named sample requests that tests can send.

Fixtures contain no assertions. Consumers decide what to check, using the
values in the fixture, which they read from the image with `print-fixture`.

## Location and validation

```text
fixtures/<name>/fixture.json
```

`make fixtures` validates each fixture against the schema, then checks the
cross-field rules:

- the name matches the directory;
- EndDevice SFDIs and LFDIs are unique;
- every DER program references a valid EndDevice;
- certificate paths reference valid entries;
- every answer is for an operation the answering role receives;
- every sample is sent by the role that initiates that operation.

The adapters check a fixture when they load it, and exit 4 on a violation.
A fixture with an operation the adapter cannot perform exits 5.

## Answers

When a server receives a request, the adapter looks up the fixture's
`answers.server.<operation>`. The `payload` field contains the JSON
representation of the IEEE 2030.5 XML response.

When a client run receives a server-initiated update (e.g., a new
DERControl via subscription notification), the adapter looks up
`answers.client.<operation>`.

An operation without an answer in the fixture returns an IEEE 2030.5 error
response (HTTP 404 or the appropriate status code).

### Answer format

```json
{
  "register": {
    "payload": {
      "status": 201,
      "href": "/edev/1"
    }
  }
}
```

### Special values

| Value | Meaning |
|-------|---------|
| `${now}` | Replaced with the current time in RFC 3339 UTC |
| `${lfdi}` | Replaced with the requesting device's LFDI |
| `${sfdi}` | Replaced with the requesting device's SFDI |

## Samples

Named requests that tests send. Each sample has:

| Field | Description |
|-------|-------------|
| `name` | Unique name for the sample |
| `operation` | The operation to perform |
| `from` | `client` or `server` |
| `payload` | The JSON payload of the request |

Example:

```json
{
  "register-device": {
    "operation": "register",
    "from": "client",
    "payload": {
      "deviceCategory": "0020",
      "lFDI": "AB0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCD",
      "sFDI": "123456789"
    }
  }
}
```

## EndDevices

Each EndDevice in the fixture has:

| Field | Type | Description |
|-------|------|-------------|
| `sfdi` | string | Short-Form Device Identifier (decimal, 1-10 digits) |
| `lfdi` | string | Long-Form Device Identifier (hex, 64 characters) |
| `deviceCategory` | string | IEEE 2030.5 DeviceCategory (hex-encoded bitfield) |
| `pin` | string | Registration PIN (for server-side verification) |

## DER programs

Each DER program describes a set of controls:

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Human-readable name |
| `primacy` | integer | Program priority (0 = highest) |
| `controls` | array | List of DERControl objects |

## Controls

Each DERControl:

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Control type: `opModConnect`, `opModEnergize`, `opModFixedW`, etc. |
| `value` | number/boolean | The control setpoint or state |
| `startTime` | string | RFC 3339 UTC start time, or `${now}` |
| `duration` | integer | Duration in seconds |

## Certificates

The fixture references TLS certificates for the test scenario:

| Field | Type | Description |
|-------|------|-------------|
| `caDir` | string | Path to the CA certificate directory |
| `deviceDir` | string | Path to the device certificate directory |

Actual certificates are not in the fixture: they are generated at test time
and mounted into the container. The fixture describes which identities
(LFDI/SFDI) the server should accept.
