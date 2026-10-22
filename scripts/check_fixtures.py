#!/usr/bin/env python3
"""Validates every fixture: the schema, then the rules a schema cannot say.

Usage: check_fixtures.py [fixtures directory]   (default: fixtures)
"""

import json
import pathlib
import sys

import jsonschema

ROOT = pathlib.Path(__file__).resolve().parent.parent


def check_digit_ok(number: int) -> bool:
    """IEEE 2030.5 PINType and SFDI: the last digit makes the digit sum a multiple of 10."""
    return sum(int(d) for d in str(number)) % 10 == 0


def problems(path: pathlib.Path, fixture: dict) -> list[str]:
    out = []
    if fixture["name"] != path.parent.name:
        out.append(f'name "{fixture["name"]}" is not the directory name "{path.parent.name}"')
    if not check_digit_ok(fixture["device"]["pin"]):
        out.append(f'device.pin {fixture["device"]["pin"]} has a wrong check digit')
    mrids = []
    for program in fixture["derPrograms"]:
        mrids.append(program["mRID"])
        if "defaultControl" in program:
            mrids.append(program["defaultControl"]["mRID"])
        mrids.extend(c["mRID"] for c in program["controls"])
        mrids.extend(c["mRID"] for c in program.get("curves", []))
    for tariff in fixture.get("tariffs", []):
        mrids.append(tariff["mRID"])
        for component in tariff["rateComponents"]:
            mrids.append(component["mRID"])
            mrids.extend(i["mRID"] for i in component["timeTariffIntervals"])
    for program in fixture.get("messages", []):
        mrids.append(program["mRID"])
        mrids.extend(m["mRID"] for m in program["textMessages"])
    mrids.append(fixture["report"]["mirrorUsagePoint"]["mRID"])
    mrids.extend(r["mRID"] for r in fixture["report"]["meterReadings"])
    if "flowReservation" in fixture:
        mrids.append(fixture["flowReservation"]["mRID"])
    duplicates = sorted({m for m in mrids if mrids.count(m) > 1})
    if duplicates:
        out.append(f"mRIDs used more than once: {', '.join(duplicates)}")
    # Servers pick their own mRIDs and hrefs, and may describe controls their
    # own way, so clients find the fixture's programs by description and its
    # controls by what they do: their DERControlBase and duration.
    programs = [p["description"] for p in fixture["derPrograms"]]
    if len(set(programs)) != len(programs):
        out.append("DER program descriptions are not unique")
    for program in fixture["derPrograms"]:
        controls = [json.dumps([c["base"], c["duration"]], sort_keys=True) for c in program["controls"]]
        if len(set(controls)) != len(controls):
            out.append(f'DER program "{program["description"]}" has two controls with the same base and duration')
        # A curve mode names one of the program's own curves, of the matching type.
        curves = {c["mRID"]: c["curveType"] for c in program.get("curves", [])}
        points = [json.dumps([c["curveType"], c["points"]]) for c in program.get("curves", [])]
        if len(set(points)) != len(points):
            out.append(f'DER program "{program["description"]}" has two curves with the same type and points')
        bases = [c["base"] for c in program["controls"]] + ([program["defaultControl"]["base"]] if "defaultControl" in program else [])
        for base in bases:
            for mode, curve_type in CURVE_MODES.items():
                if mode in base and curves.get(base[mode]) != curve_type:
                    out.append(f'DER program "{program["description"]}": {mode} names {base[mode]}, '
                               f"which is not a curve of type {curve_type} of the program")
    # Clients find tariffs and their rate components by description, and
    # intervals by duration and price, as they find programs and controls.
    for kind, items, children in (("tariff", fixture.get("tariffs", []), "rateComponents"),
                                  ("messaging program", fixture.get("messages", []), "textMessages")):
        if len({i["description"] for i in items}) != len(items):
            out.append(f"{kind} descriptions are not unique")
        for item in items:
            if len({c["description"] for c in item[children]}) != len(item[children]):
                out.append(f'{kind} "{item["description"]}" has two {children} with the same description')
    for tariff in fixture.get("tariffs", []):
        for component in tariff["rateComponents"]:
            intervals = [(i["duration"], i["price"]) for i in component["timeTariffIntervals"]]
            if len(set(intervals)) != len(intervals):
                out.append(f'rate component "{component["description"]}" has two intervals with the same duration and price')
    return out


# DERControlBase curve modes and the DERCurveType each must name.
CURVE_MODES = {"opModFreqWatt": 0, "opModVoltVar": 11, "opModVoltWatt": 12, "opModWattPF": 13}


def main() -> int:
    directory = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / "fixtures"
    schema = json.loads((ROOT / "schemas" / "fixture.schema.json").read_text())
    jsonschema.Draft202012Validator.check_schema(schema)
    validator = jsonschema.Draft202012Validator(schema)
    failed = False
    paths = sorted(directory.glob("*/fixture.json"))
    if not paths:
        print(f"no fixtures in {directory}", file=sys.stderr)
        return 1
    for path in paths:
        fixture = json.loads(path.read_text())
        errors = [f"{'/'.join(map(str, e.absolute_path)) or '(root)'}: {e.message}" for e in validator.iter_errors(fixture)]
        if not errors:
            errors = problems(path, fixture)
        for e in errors:
            print(f"FAIL {path.relative_to(ROOT)}: {e}")
        failed = failed or bool(errors)
        if not errors:
            print(f"ok   {path.relative_to(ROOT)}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
