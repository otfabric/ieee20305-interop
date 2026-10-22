# SPDX-License-Identifier: MIT
"""Copies the licence files of every installed distribution to <dir>/<name>/.

Usage: collect_licenses.py <dir> [<distribution to skip> ...]

Run with the virtual environment's python at image build time; the
distributions to skip are the upstream itself, whose licence the Dockerfile
copies from its source. Also writes
<dir>/BUNDLED-MODULES.txt (name and version of each distribution). Fails
when a distribution has no licence file.
"""

import shutil
import sys
from importlib import metadata
from pathlib import Path

LICENSE_PREFIXES = ("LICENSE", "LICENCE", "COPYING", "NOTICE", "AUTHORS")


def main(out: Path, skip: set[str]) -> int:
    out.mkdir(parents=True, exist_ok=True)
    bundled, missing = [], []
    for dist in sorted(metadata.distributions(), key=lambda d: d.metadata["Name"].lower()):
        name, version = dist.metadata["Name"], dist.version
        if name.lower() in skip | {"pip"}:
            continue
        bundled.append(f"{name} {version}")
        files = [f for f in dist.files or [] if Path(f.name).name.upper().startswith(LICENSE_PREFIXES)]
        if not files:
            missing.append(name)
            continue
        target = out / name
        target.mkdir(parents=True, exist_ok=True)
        for f in files:
            shutil.copy(dist.locate_file(f), target / Path(f.name).name)
    (out / "BUNDLED-MODULES.txt").write_text("\n".join(bundled) + "\n")
    if missing:
        print("no licence file in: " + ", ".join(missing), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(Path(sys.argv[1]), {name.lower() for name in sys.argv[2:]}))
