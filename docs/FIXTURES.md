# Fixtures

A fixture describes one IEEE 2030.5 scenario:

- the device: the serial in its certificate, its registration PIN and its
  device category;
- the DER programs, with their default control and controls, that a server
  is seeded with for that device;
- the metering a client reports.

A fixture seeds state; it does not script answers. A server provisions and
seeds itself from the fixture when it starts, then answers every request
itself (see [the contract](CONTAINER_CONTRACT.md#how-ieee-20305-shapes-this-contract)).
A client uses the fixture for what it sends and for what it expects to find.

Fixtures contain no assertions. Consumers decide what to check, using the
values in the fixture, which they read from the image with `print-fixture`.

## Location and validation

```text
fixtures/<name>/fixture.json
```

`make fixtures` ([scripts/check_fixtures.py](../scripts/check_fixtures.py))
validates each fixture against
[schemas/fixture.schema.json](../schemas/fixture.schema.json), then checks:

- the name is the directory name;
- the PIN has a valid check digit;
- every mRID is used once;
- DER program descriptions are unique, and no program has two controls with
  the same DERControlBase and duration.

The adapters check a fixture when they load it, and exit 4 on a violation.
A fixture the adapter cannot seed or send exits 5.

## The baseline fixture

[fixtures/baseline/fixture.json](../fixtures/baseline/fixture.json): one
device, one DER program (primacy 1) with two scheduled controls (connect and
energize for an hour from the start; then disconnect for half an hour), and
one MirrorUsagePoint with a real-power reading.

The baseline holds only what every server adapter can be seeded with:
`opModConnect` and `opModEnergize` are base IEEE 2030.5 and CSIP-AUS alike,
and it has no `defaultControl`, because Envoy's defaults are CSIP-AUS limits
only. A fixture that needs more is for the adapters that declare it; the
others exit 5.

## Fields

### `device`

| Field | Type | Description |
|-------|------|-------------|
| `serial` | string | Serial number in the RFC 4108 HardwareModuleName of the device certificate. The tests generate a certificate for it; its LFDI and SFDI follow from the certificate, so they are not in the fixture |
| `pin` | integer | Registration PIN (PINType): five digits and a check digit. A server registers the device with it; a client checks it |
| `deviceCategory` | string | DeviceCategoryType bitmap, 8 upper-case hex digits |

### `derPrograms[]`

| Field | Type | Description |
|-------|------|-------------|
| `mRID` | string | 32 upper-case hex digits |
| `description` | string | At most 32 characters (String32); how clients find the program |
| `primacy` | integer | 0–255; lower is higher priority |
| `defaultControl` | object | Optional DefaultDERControl: `mRID`, `description`, `base` |
| `controls` | array | DERControls: `mRID`, `description`, `base`, `duration` (seconds) and `start` (seconds after the server starts; default 0) |

`base` is a DERControlBase with at least one of:

| Element | Type | Value |
|---------|------|-------|
| `opModConnect` | boolean | |
| `opModEnergize` | boolean | |
| `opModMaxLimW` | integer | PerCent: hundredths of a percent of the maximum active power, 0–10000 |
| `opModFixedW` | integer | SignedPerCent, -10000–10000 |

A server is free to give the programs and controls its own mRIDs, hrefs and,
for controls, descriptions (Envoy describes a control by its start time). A
client finds a program by its description and a control by its `base` and
`duration`.

### `report`

| Field | Type | Description |
|-------|------|-------------|
| `mirrorUsagePoint` | object | `mRID`, `description`, `roleFlags` (4 hex digits), `serviceCategoryKind`, `status` |
| `meterReadings[]` | array | MirrorMeterReadings: `mRID`, `description`, `readingType` (ReadingType elements by their IEEE 2030.5 names; `commodity`, `kind` and `uom` required) and `value` |

A client sends the reading with the current time.

## TLS material

Certificates are never part of a fixture. The tests generate them at run
time for the fixture's serial (`make_pki` in
[scripts/lib.sh](../scripts/lib.sh)) and mount them; see
[Transport security](CONTAINER_CONTRACT.md#transport-security).
