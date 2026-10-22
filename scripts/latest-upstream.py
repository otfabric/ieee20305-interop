#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Compares one upstream pin with the latest upstream release.

    scripts/latest-upstream.py <stack>

A stack is an upstream an adapter pins: the adapter's own upstream, or a
second one its image builds (gridappsd-client). Prints key=value lines (the
format of $GITHUB_OUTPUT):

    adapter=<the adapter that builds the stack>
    pin=<the version the adapter Dockerfile pins>
    built=yes|no          whether adapters/<adapter>/Dockerfile exists
    latest=<latest stable upstream release>
    latest_sha=<the commit of its git tag>
    update=yes|no
    candidate_args=<the build arguments of make candidate-<adapter>>
    source=<where the latest release was looked up>

Nothing is modified. Pre-releases (rc, beta, ...) are never "latest".
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# stack: (adapter, version build argument, commit build argument, git repository)
STACKS = {
    "gridappsd": ("gridappsd", "UPSTREAM_VERSION", "UPSTREAM_SHA",
                  "https://github.com/GRIDAPPSD/ieee-2030_5-server-go.git"),
    "gridappsd-client": ("gridappsd", "CLIENT_VERSION", "CLIENT_SHA",
                         "https://github.com/GRIDAPPSD/ieee-2030_5-client-go.git"),
    "py20305": ("py20305", "UPSTREAM_VERSION", "UPSTREAM_SHA", "https://github.com/DERSecurity/py20305.git"),
    "envoy": ("envoy", "UPSTREAM_VERSION", "UPSTREAM_SHA", "https://github.com/bsgip/envoy.git"),
    "epri": ("epri", "UPSTREAM_VERSION", "UPSTREAM_SHA", "https://github.com/epri-dev/IEEE-2030.5-Client.git"),
}


def key(version):
    """Sort key of a stable version; None for anything else (pre-releases)."""
    match = re.fullmatch(r"v?(\d+(?:\.\d+)*)", version)
    return tuple(int(part) for part in match.group(1).split(".")) if match else None


def git_tags(repository):
    """Tags of repository as {tag: commit}; an annotated tag gives its commit."""
    out = subprocess.check_output(["git", "ls-remote", "--tags", repository], text=True)
    commits = {}
    for line in out.splitlines():
        sha, ref = line.split()
        tag = ref.removeprefix("refs/tags/")
        if tag.endswith("^{}"):
            commits[tag[:-3]] = sha
        else:
            commits.setdefault(tag, sha)
    return commits


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in STACKS:
        raise SystemExit(f"usage: latest-upstream.py {'|'.join(STACKS)}")
    adapter, version_arg, sha_arg, repository = STACKS[sys.argv[1]]
    dockerfile = ROOT / "adapters" / adapter / "Dockerfile"
    if not dockerfile.exists():
        raise SystemExit(f"adapters/{adapter}/Dockerfile does not exist")
    match = re.search(rf"^ARG {version_arg}=(.+)$", dockerfile.read_text(), re.M)
    if not match:
        raise SystemExit(f"adapters/{adapter}/Dockerfile has no ARG {version_arg}=<pin>")
    pin = match.group(1).strip()
    tags = git_tags(repository)
    stable = [tag for tag in tags if key(tag) is not None]
    if not stable:
        raise SystemExit(f"no stable release tag in {repository}")
    latest = max(stable, key=key)
    print(f"adapter={adapter}")
    print(f"pin={pin}")
    print("built=yes")
    print(f"latest={latest}")
    print(f"latest_sha={tags[latest]}")
    print(f"update={'yes' if key(latest) != key(pin) else 'no'}")
    print(f"candidate_args={version_arg}={latest} {sha_arg}={tags[latest]}")
    print(f"source=git tags of {repository.removeprefix('https://github.com/').removesuffix('.git')}")


if __name__ == "__main__":
    main()
