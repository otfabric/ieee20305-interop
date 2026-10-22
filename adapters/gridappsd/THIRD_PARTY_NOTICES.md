# Third-party notices: gridappsd adapter image

This image contains:

| Component | Version | Licence | Licence files in this image |
|---|---|---|---|
| ieee20305-interop adapter (`/usr/local/bin/ieee20305-interop`) | this repository | MIT | `/licenses/ieee20305-interop/LICENSE` |
| GridAPPS-D [ieee-2030_5-server-go](https://github.com/GRIDAPPSD/ieee-2030_5-server-go) (`/usr/local/bin/sep2server`) | see `versions.yaml` | Battelle Memorial Institute BSD-style licence | `/licenses/github.com/GRIDAPPSD/ieee-2030_5-server-go/` (LICENSE, NOTICE) |
| GridAPPS-D [ieee-2030_5-client-go](https://github.com/GRIDAPPSD/ieee-2030_5-client-go), compiled into the adapter | see `versions.yaml` | Battelle Memorial Institute BSD-style licence | `/licenses/github.com/GRIDAPPSD/ieee-2030_5-client-go/` (LICENSE, NOTICE) |
| Go modules compiled into either binary | listed in `/licenses/BUNDLED-MODULES.txt` | per module | `/licenses/<module path>/` |
| The Go standard library | the toolchain of the build image | BSD-3-Clause | `/licenses/go/LICENSE` |
| Debian base image packages | `debian:trixie-slim` | per package | `/usr/share/doc/*/copyright` |

The GridAPPS-D NOTICE files describe IEEE material those projects refer to
but do not distribute. This image does not contain the IEEE 2030.5 schema or
WADL either.
