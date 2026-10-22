# Fixtures

Fixture schema **1.1** (`fixture_schema` in [versions.yaml](../versions.yaml)).
Fixtures carry `schemaVersion` `"1.0"`, the compatible line of the
contract's documents: 1.1 only adds optional members.

A fixture describes one IEEE 2030.5 scenario:

- the device: the serial in its certificate, its registration PIN and its
  device category;
- what a server is seeded with for that device: DER programs with their
  default controls, controls and curves; tariffs; messages;
- what a client sends: the metering it reports, and its DER, a log event
  and a flow reservation.

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
  the same DERControlBase and duration, or two curves with the same type
  and points;
- a curve mode of a control names a curve of the same program, of the
  matching curveType;
- tariff and messaging-program descriptions are unique, as are the
  descriptions of their rate components and text messages, and no rate
  component has two intervals with the same duration and price.

The adapters check a fixture when they load it, and exit 4 on a violation.
A fixture the adapter cannot seed or send exits 5.

## Baked fixtures

Every baked fixture has the same device (serial and PIN), so that one
generated PKI serves them all. The capabilities of an adapter name the
fixtures each role runs with (`fixtures`); a server started with another is
exit 5.

| Fixture | For | What it adds to the baseline |
|---------|-----|------------------------------|
| `baseline` | every server and client | |
| `csip` | servers that issue base IEEE 2030.5 functions beyond connect and energize (GridAPPS-D) | two programs (primacy 1 and 2), each with a default control; `opModMaxLimW`, `opModFixedW` |
| `csip-aus` | CSIP-AUS servers (Envoy) | two programs whose default controls and controls are dynamic operating envelopes (`opModExpLimW`, `opModImpLimW`, `opModGenLimW`, `opModLoadLimW`); a time-of-use tariff with an import and an export rate component |

### The baseline fixture

[fixtures/baseline/fixture.json](../fixtures/baseline/fixture.json): one
device, one DER program (primacy 1) with two scheduled controls (connect and
energize for an hour from the start; then disconnect for half an hour), and
one MirrorUsagePoint with a real-power reading.

The baseline holds only what every server adapter can be seeded with:
`opModConnect` and `opModEnergize` are base IEEE 2030.5 and CSIP-AUS alike,
and it has no `defaultControl`, because Envoy's defaults are CSIP-AUS limits
only and GridAPPS-D's cannot be CSIP-AUS limits. A fixture that needs more is
for the adapters that declare it; the others exit 5. It also holds what a
client sends for `put-der`, `log-event` and `flow-reservation`.

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
| `defaultControl` | object | Optional DefaultDERControl: `mRID`, `description`, `base`. A client matches it by `base` (servers need not serve the description: DefaultDERControl has none in IEEE 2030.5-2018) |
| `controls` | array | DERControls: `mRID`, `description`, `base`, `duration` (seconds) and `start` (seconds after the server starts; default 0) |
| `curves` | array | Optional DERCurves (no baked fixture has curves yet: no server adapter can be seeded with them and serve them): `mRID`, `description`, `curveType` (DERCurveType), `xMultiplier`, `yMultiplier`, `yRefType` (default 0), `points` (1–10 `{x, y}`) |

Several programs are allowed; a client finds each by its description, and a
server that serves fewer programs than the fixture has (one per device, say)
exits 5.

`base` is a DERControlBase with at least one of:

| Element | Type | Value |
|---------|------|-------|
| `opModConnect` | boolean | |
| `opModEnergize` | boolean | |
| `opModMaxLimW` | integer | PerCent: hundredths of a percent of the maximum active power, 0–10000 |
| `opModFixedW` | integer | SignedPerCent, -10000–10000 |
| `opModExpLimW`, `opModImpLimW`, `opModGenLimW`, `opModLoadLimW` | object | CSIP-AUS export, import, generation and load limits (CSIP-AUS 1.3 namespace `https://csipaus.org/ns/v1.3`, as Envoy serves them), as ActivePower `{value, multiplier}`: value × 10^multiplier W |
| `opModVoltVar`, `opModVoltWatt`, `opModFreqWatt`, `opModWattPF` | string | The mRID of a curve of the program, of curveType 11, 12, 0 and 13; served as a DERCurveLink. A client matches it by the curve it links to |

A server that represents a limit in another multiplier serves the same
power; a client compares the power (value × 10^multiplier).

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

### `tariffs[]` (optional)

| Field | Type | Description |
|-------|------|-------------|
| `mRID`, `description` | | TariffProfile; clients find it by description |
| `currency` | integer | ISO 4217 numeric code (36 AUD, 840 USD) |
| `pricePowerOfTenMultiplier` | integer | -9–9 |
| `primacy` | integer | 0–255 |
| `rateCode` | string | Optional, at most 20 characters |
| `rateComponents[]` | array | `mRID`, `description` (how clients find it), `roleFlags` (4 hex digits), `flowDirection` (of its ReadingType: 1 import, 19 export) and `timeTariffIntervals[]`: `mRID`, `description`, `start` (seconds after the server starts), `duration` and `price`, the price of its ConsumptionTariffInterval in 10^`pricePowerOfTenMultiplier` units of the currency per kWh. Clients find an interval by duration and price; servers need not serve its description |

### `messages[]` (optional)

MessagingPrograms: `mRID`, `description`, `locale` (such as `en-US`),
`primacy` and `textMessages[]` (`mRID`, `description`, `originator`,
`priority` 0–3, `textMessage`, `start`, `duration`). No server adapter can be
seeded with messages yet, and no client operation reads them.

### What a client sends (optional)

| Field | Used by | Content |
|-------|---------|---------|
| `der` | `put-der` | `capability` (`modesSupported` DERControlType bitmap as 8 hex digits, `rtgMaxW`, `type` DERType, and optionally the CSIP-AUS `doeModesSupported`, 2 hex digits), `settings` (`modesEnabled`, `setMaxW`, `setGradW`, optionally the CSIP-AUS `doeModesEnabled`), `status` (`genConnectStatus` 2 hex digits, `operationalModeStatus`), `availability` (`availabilityDuration` seconds, `statWAvail`); powers as ActivePower |
| `logEvent` | `log-event` | `logEventCode`, `logEventID`, `logEventPEN`, `functionSet`, `profileID`, `details` |
| `flowReservation` | `flow-reservation` | `mRID`, `description`, `energyRequested` (SignedRealEnergy `{value, multiplier}` Wh), `powerRequested` (ActivePower), `start` (seconds after the request), `duration`, `durationRequested` |

A client without the member an operation needs returns `invalid-request`
(exit 1). A client whose upstream cannot send an element says so in its
capability notes.

## TLS material

Certificates are never part of a fixture. The tests generate them at run
time for the fixture's serial (`make_pki` in
[scripts/lib.sh](../scripts/lib.sh)) and mount them; see
[Transport security](CONTAINER_CONTRACT.md#transport-security).
