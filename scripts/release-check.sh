#!/usr/bin/env bash
# Checks that the repository is ready to be released as VERSION (vX.Y.Z):
# versions.yaml says so, and RELEASE.md has a dated section for it, ended by
# a --- line. The Release workflow runs this on the tag; run it before
# tagging with
#   make release-check VERSION=vX.Y.Z
# and prepare both with make release-prep VERSION=vX.Y.Z.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

version="${1:-}"
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "usage: release-check.sh vX.Y.Z" >&2; exit 2; }
status=0
want="interop_version: \"${version#v}\""
if ! grep -qF "${want}" versions.yaml; then
    echo "FAIL versions.yaml does not say ${want}"
    status=1
fi
# The version's notes: from its heading up to the --- that ends it, exactly
# what the Release workflow publishes (scripts/release-notes.sh).
if ! section=$(scripts/release-notes.sh "${version}" 2>&1); then
    echo "FAIL ${section}"
    status=1
elif ! grep -qE '^\*\*Date:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}' <<<"${section}"; then
    echo "FAIL the RELEASE.md section of ${version} is not dated (run make release-prep VERSION=${version})"
    status=1
fi
[[ "${status}" -eq 0 ]] && echo "ok   ready to release ${version}"
exit "${status}"
