#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Writes manifest.json and the body of the GitHub release.

    scripts/release-manifest.py <version> <commit> <digests file> <manifest out> <body out>

The digests file holds one "<adapter> <sha256:...>" line per published
multi-arch image (make digests); the release workflow refuses an empty one.
Upstream pins are read from the adapter Dockerfiles, the contract and fixture
versions from versions.yaml, and the notes from the version's section of
RELEASE.md, up to the --- that ends it.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REGISTRY = "ghcr.io/otfabric/ieee20305-interop-"
# Build arguments that pin an upstream, and how manifest.json names them.
PINS = {
    "UPSTREAM_VERSION": ("upstream", "version"),
    "UPSTREAM_SHA": ("upstream", "revision"),
    "CLIENT_VERSION": ("client", "version"),
    "CLIENT_SHA": ("client", "revision"),
}


def pins(adapter: str) -> dict[str, dict[str, str]]:
    text = (ROOT / "adapters" / adapter / "Dockerfile").read_text()
    out: dict[str, dict[str, str]] = {}
    for arg, (which, field) in PINS.items():
        match = re.search(rf"^ARG {arg}=(.+)$", text, re.M)
        if match:
            out.setdefault(which, {})[field] = match.group(1).strip()
    if "version" not in out.get("upstream", {}):
        raise SystemExit(f"adapters/{adapter}/Dockerfile has no ARG UPSTREAM_VERSION")
    return out


def manifest_value(key: str) -> str:
    match = re.search(rf'^{key}: "([^"]+)"', (ROOT / "versions.yaml").read_text(), re.M)
    if not match:
        raise SystemExit(f"versions.yaml has no {key}")
    return match.group(1)


def notes(version: str) -> str:
    match = re.search(rf"^## {re.escape(version)}\n(.*?)^---$", (ROOT / "RELEASE.md").read_text(), re.M | re.S)
    if not match:
        raise SystemExit(f"RELEASE.md has no section {version} ended by ---")
    return match.group(1).strip() + "\n"


def main() -> None:
    if len(sys.argv) != 6:
        raise SystemExit(__doc__)
    version, commit, digests_file, manifest_out, body_out = sys.argv[1:]
    if not re.fullmatch(r"v\d+\.\d+\.\d+", version):
        raise SystemExit(f"{version!r} is not a vX.Y.Z version")
    if manifest_value("interop_version") != version[1:]:
        raise SystemExit(f"versions.yaml interop_version is not {version[1:]}")
    digests = dict(line.split() for line in Path(digests_file).read_text().splitlines() if line.strip())
    if not digests:
        raise SystemExit("no image digests")

    upstream, images = {}, {}
    for adapter, digest in digests.items():
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise SystemExit(f"{adapter}: {digest!r} is not a digest")
        p = pins(adapter)
        upstream[adapter] = {**p["upstream"], **({"client": p["client"]} if "client" in p else {})}
        images[adapter] = f"{REGISTRY}{adapter}@{digest}"

    Path(manifest_out).write_text(json.dumps({
        "version": version,
        "commit": commit,
        "contract": manifest_value("contract_version"),
        "fixtureSchema": manifest_value("fixture_schema"),
        "platforms": ["linux/amd64", "linux/arm64"],
        "upstream": upstream,
        "images": images,
    }, indent=2) + "\n")

    body = [notes(version), "## Images", "", "| Image | Upstream | Digest |", "|-------|----------|--------|"]
    for adapter, digest in digests.items():
        pin = upstream[adapter]["version"]
        if "client" in upstream[adapter]:
            pin += f" (client {upstream[adapter]['client']['version']})"
        body.append(f"| `{REGISTRY}{adapter}:{version}` | {pin} | `{digest}` |")
    body += ["", "## Pinning in a consumer", "", "```bash"]
    for adapter, image in images.items():
        body.append(f"IEEE20305_INTEROP_{adapter.upper()}_IMAGE={image}")
    body += [
        "```",
        "",
        "`manifest.json` records the upstream versions and commits these images were built from. "
        "The images contain third-party software under its own licences; see "
        "`THIRD_PARTY_NOTICES.md` and `/licenses` in each image.",
        "",
    ]
    Path(body_out).write_text("\n".join(body))


if __name__ == "__main__":
    main()
