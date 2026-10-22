# Third-party notices: ieee20305-interop-py20305

This image contains the ieee20305-interop adapter (MIT, see
`/licenses/ieee20305-interop/LICENSE`) and the following third-party
software, each under its own licence.

## py20305

- Upstream: https://github.com/DERSecurity/py20305 (DER Security Corp)
- Version: v0.16.0, commit 79844938eea3d05f5432cdc5f2d07a103e87912b
- Licence: Apache-2.0, with a NOTICE file:
  `/licenses/github.com/DERSecurity/py20305/`
- Installed from that source, unmodified, in `/opt/venv`. The package
  includes the IEEE 2030.5 and CSIP-AUS XML schemas as py20305 distributes
  them; this repository does not add or change them.

## Python packages

The dependencies of py20305, installed from PyPI with the hashes of
`adapters/py20305/requirements.txt`. Their names and versions are in
`/licenses/BUNDLED-MODULES.txt` and their licence files in
`/licenses/<name>/`.

## Python and Debian

The base image `python:3.13-slim-trixie` (pinned by digest) carries CPython
(PSF licence) and Debian packages under their own licences, listed in
`/usr/share/doc/*/copyright`.
