# ieee20305-interop-otfabric

[go-ieee20305](https://github.com/otfabric/go-ieee20305), OT Fabric's own IEEE
2030.5 stack, behind the [container contract](../../docs/CONTAINER_CONTRACT.md),
so that other stacks can test against it. Anything that uses an
`ieee20305-interop-<adapter>` image can use this one in its place. Unlike those,
it is not a reference: its tags are go-ieee20305 versions, and no harness release
or `manifest.json` contains it.

**For testing only.** The server sends notifications to any notificationURI a
device gives it, including private addresses, which a real deployment refuses.
Do not expose it to a network you do not control. Old images stay available, so
check [go-ieee20305 advisories](https://github.com/otfabric/go-ieee20305/security/advisories)
for the version you pull.

**Not an independent implementation.** The image declares
`upstream.independentEngine: false`, and `results.json` records
`independent: false`. A result against it says nothing independent about
go-ieee20305 when OT Fabric produces it. A result you publish, naming the
implementation and version, is independent evidence, and we would like to hear
about it.

## Use

```sh
make pki    # ./tls, from the repository root
docker run -d --name go-ieee20305 -p 127.0.0.1:8443:443 -v "$PWD/tls:/tls:ro" \
    ghcr.io/otfabric/ieee20305-interop-otfabric:<tag> server --version 2018 --tls-dir /tls/server
until docker exec go-ieee20305 test -f /run/ieee20305-interop/ready; do sleep 0.1; done
```

`<tag>` is a go-ieee20305 version such as `v0.6.0`, or `latest`, the newest one
published. A rebuild of a version replaces its tag, so pin the digest if you need
an exact build. The digest is in the summary of the
[workflow run](https://github.com/otfabric/ieee20305-interop/actions/workflows/own-go-ieee20305.yml),
and `docker run --rm <image> version` prints the versions.

| | |
|---|---|
| Upstream | go-ieee20305, the latest release when the image was built, with its `tlsccm` module |
| Licence | MIT; every module's licence and notice files are in the image under `/licenses` |
| Adapter name | `otfabric` (the name in documents, as the schema allows) |
| Language | Go |
| Protocol engine | OT Fabric's own: the `server` and `client` packages |
| TLS | TLS 1.2, `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8` and `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`, in both roles |

## Why it is apart from the other adapters

`REQUIREMENTS.md` R1: nothing under `adapters/`, `scripts/`, `tests/` or the
top-level `Makefile` knows a library under test, and CI enforces that. This
directory and `.github/workflows/own-go-ieee20305.yml` are the one exception.
The scripts take the image through `ADAPTER_IMAGE_otfabric`.

No release loop can form (`otfabric/engineering` ADR-0012). This image is built
by hand, from the latest go-ieee20305 release, and nothing is committed: this
repository has no pin of go-ieee20305 to bump. No release or required check of
go-ieee20305 needs the image, and go-ieee20305 commits no pin of it. It is not in
a harness release or `manifest.json`.

## Publishing

Run the **Own stack (go-ieee20305)** workflow by hand, from `main`. It finds the
latest go-ieee20305 release and the latest harness release, and prints them. It
builds the image, runs the smoke test and the cross-stack self-test against the
references of that harness release on both architectures, then publishes
`:<go-ieee20305 version>` and `:latest`. Run from a branch, it stops before
publishing. Nothing runs it automatically, so after a go-ieee20305 release the
image stays on the old version until someone does.

```sh
make -f own/go-ieee20305/Makefile image smoke interop    # from the repository root
```

## What it does

Both roles, IEEE 2030.5-2018 and 2023, every operation of the contract, and the
fixtures `baseline` and `csip` (and `csip-aus` in 2018, which CSIP-AUS profiles).

- **Server**: the library's server with its in-memory store. It provisions the
  devices of `--tls-dir/devices` with the fixture's PIN, a DER, one
  FunctionSetAssignments with the fixture's programs, default controls,
  controls and tariffs; a CSIP-AUS fixture is served with the `csipaus`
  profile. In-band registration is on: an unprovisioned device may create its
  EndDevice but gets no Registration. `call issue-control` issues a control and
  writes its program again, so that the subscribers of the DERProgramList are
  notified (the library notifies a list's subscribers of changes of its
  members). The adapter writes `device-registered` and `subscription-created`
  for the server's 201 answers.
- **Client**: the library's client. `subscribe` serves its notification
  listener with crypto/tls on `notify.crt`, and verifies the server's
  certificate against `ca.crt` when the server presents one (GridAPPS-D's
  sep2server delivers without one). `put-der` in 2023 sends the DER resources
  without the CSIP-AUS elements of the fixture.

## What it cannot do

- **Curves and messages** (`curves`, `messaging`: `false`): the library models
  them, but the adapter neither seeds nor checks them, and no baked fixture has
  them. A fixture with curves or messages is exit 5.
- **`client run`** is not implemented by any adapter (exit 5).
