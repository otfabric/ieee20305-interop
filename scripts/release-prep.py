#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Prepares a release: dates the "## vX.Y.Z" section of RELEASE.md, whose
date reads "unreleased", with today's date, and sets interop_version in
versions.yaml.

Usage: release-prep.py vX.Y.Z   (or make release-prep VERSION=vX.Y.Z)

Review the diff, commit it, merge it, then tag the merged commit.
"""

import datetime
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def main(argv: list[str]) -> int:
    if len(argv) != 1 or not re.fullmatch(r"v\d+\.\d+\.\d+", argv[0]):
        print(__doc__, file=sys.stderr)
        return 2
    version = argv[0]
    notes = ROOT / "RELEASE.md"
    text = notes.read_text()
    section = re.search(rf"^## {re.escape(version)}\n(.*?)^---$", text, re.M | re.S)
    if not section:
        print(f"RELEASE.md has no section \"## {version}\" ended by ---: write its notes first", file=sys.stderr)
        return 1
    today = datetime.date.today().isoformat()
    body, n = re.subn(r"^\*\*Date:\*\* unreleased", f"**Date:** {today}", section.group(1), count=1, flags=re.M)
    if n:
        notes.write_text(text[: section.start(1)] + body + text[section.end(1):])
        print(f"RELEASE.md: {version} dated {today}")
    else:
        print(f"RELEASE.md: {version} is already dated")
    versions = ROOT / "versions.yaml"
    new, n = re.subn(r'^interop_version: "[^"]*"', f'interop_version: "{version[1:]}"', versions.read_text(),
                     count=1, flags=re.M)
    if n != 1:
        print("versions.yaml has no interop_version line", file=sys.stderr)
        return 1
    versions.write_text(new)
    print(f"versions.yaml: interop_version {version[1:]}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
