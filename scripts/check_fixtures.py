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
    mrids.append(fixture["report"]["mirrorUsagePoint"]["mRID"])
    mrids.extend(r["mRID"] for r in fixture["report"]["meterReadings"])
    duplicates = sorted({m for m in mrids if mrids.count(m) > 1})
    if duplicates:
        out.append(f"mRIDs used more than once: {', '.join(duplicates)}")
    # Servers pick their own mRIDs and hrefs, so clients find the fixture's
    # programs and controls by description.
    programs = [p["description"] for p in fixture["derPrograms"]]
    if len(set(programs)) != len(programs):
        out.append("DER program descriptions are not unique")
    for program in fixture["derPrograms"]:
        controls = [c["description"] for c in program["controls"]]
        if len(set(controls)) != len(controls):
            out.append(f'control descriptions of DER program "{program["description"]}" are not unique')
    return out


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
