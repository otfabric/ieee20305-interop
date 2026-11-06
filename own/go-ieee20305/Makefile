# The ieee20305-interop-otfabric image, from the repository root:
#   make -f own/go-ieee20305/Makefile image smoke interop
#
# Apart from the top-level Makefile on purpose: this image is not in ADAPTERS
# and not in a release of the harness.

IMAGE ?= ghcr.io/otfabric/ieee20305-interop-otfabric:dev
# The harness release of the reference images to test against.
VERSION ?= $(shell git tag -l 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -1)
REFERENCES ?= gridappsd py20305 envoy epri
BASE_REGISTRY ?= docker.io/library

# The latest go-ieee20305 release, as the workflow builds it.
UPSTREAM_VERSION ?= $(shell git ls-remote --tags --refs https://github.com/otfabric/go-ieee20305.git 'v[0-9]*' | sed 's,.*refs/tags/,,' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | sort -V | tail -1)
# An annotated tag has a peeled line, with the commit.
UPSTREAM_SHA ?= $(shell r=https://github.com/otfabric/go-ieee20305.git; s=$$(git ls-remote --tags $$r 'refs/tags/$(UPSTREAM_VERSION)^{}' | cut -f1); [ -n "$$s" ] || s=$$(git ls-remote --tags $$r 'refs/tags/$(UPSTREAM_VERSION)' | cut -f1); echo $$s)

export VERSION
export ADAPTER_IMAGE_otfabric = $(IMAGE)

.PHONY: image smoke interop

image:
	docker build --build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg UPSTREAM_VERSION=$(UPSTREAM_VERSION) --build-arg UPSTREAM_SHA=$(UPSTREAM_SHA) --build-arg ADAPTER_VERSION=$(UPSTREAM_VERSION) --tag $(IMAGE) --file own/go-ieee20305/Dockerfile .

smoke:
	scripts/smoke.sh otfabric

interop:
	scripts/interop.sh otfabric $(REFERENCES)
