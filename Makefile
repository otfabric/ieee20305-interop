# Self-documented Makefile (https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html)
# Run 'make' or 'make help' to list targets.
#
# Pre-push gate:  make ci
# Quick check:    make images smoke

.DEFAULT_GOAL := help

REGISTRY  ?= ghcr.io/otfabric
VERSION   ?= dev
PLATFORMS ?= linux/amd64,linux/arm64

# Registry of the base images. They are pinned by digest in the Dockerfiles,
# so any mirror of Docker Hub's official images gives the same build. The
# workflows set BASE_REGISTRY=mirror.gcr.io/library because anonymous pulls
# from Docker Hub are rate limited per runner address.
BASE_REGISTRY ?= docker.io/library

# Registry of the images the tooling itself runs: BuildKit, the QEMU handlers,
# shellcheck, Python. The workflows set TOOLS_REGISTRY=mirror.gcr.io.
TOOLS_REGISTRY ?= docker.io

# Everything that pulls from or pushes to a registry goes through this: it
# repeats a command that failed on a rate limit, a 5xx or a timeout, and only
# then. See scripts/retry.sh.
RETRY := scripts/retry.sh

PYTHON ?= python3

# Name of the docker-container builder "make buildx-setup" creates.
BUILDER ?= ieee20305-interop

# Adapters with an image, in priority order. The self-tests run over all of
# them; override to work on one: make smoke ADAPTERS=epri
ADAPTERS ?= gridappsd py20305 envoy epri

image = $(REGISTRY)/ieee20305-interop-$(1)

export REGISTRY VERSION

# image-%, smoke-%, run-% and candidate-% are pattern rules, which make
# ignores for .PHONY targets: they depend on FORCE instead.
.PHONY: help all ci validate versions schemas fixtures lint images smoke interop \
        capabilities pki lock-py20305 lock-envoy buildx-setup buildx publish publish-arch publish-manifest digests documents run-% clean FORCE
FORCE:

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_%-]+:.*?## / {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: validate images ## Validate, and build the local images

ci: validate lint images ## Full local CI: validate, lint, build, smoke and cross-stack self-test
	@echo ""
	@echo "All local CI checks passed."

# ── Validation ───────────────────────────────────────────────────────────────

validate: versions schemas fixtures ## Validate versions.yaml, the schemas and every fixture

versions: ## Check that versions.yaml agrees with the Dockerfiles
	@scripts/check-versions.sh

schemas: ## Check that every JSON schema is a valid draft 2020-12 schema
	@echo "Checking schemas..."
	@if $(PYTHON) -c 'import jsonschema' 2>/dev/null; then \
		$(PYTHON) -c "import json, jsonschema, pathlib; \
			[jsonschema.Draft202012Validator.check_schema(json.loads(p.read_text())) for p in pathlib.Path('schemas').glob('*.json')]"; \
	else \
		echo "(jsonschema not installed locally: running in a container)"; \
		$(RETRY) docker run --rm -v "$$(pwd):/work:ro" -w /work $(TOOLS_REGISTRY)/library/python:3.12-slim \
			sh -c 'pip install -q --root-user-action=ignore jsonschema && python -c "import json, jsonschema, pathlib; \
			[jsonschema.Draft202012Validator.check_schema(json.loads(p.read_text())) for p in pathlib.Path(\"schemas\").glob(\"*.json\")]"'; \
	fi

fixtures: ## Validate every fixture against the schema and the cross-field rules
	@echo "Validating fixtures..."
	@if $(PYTHON) -c 'import jsonschema' 2>/dev/null; then \
		$(PYTHON) scripts/check_fixtures.py; \
	else \
		echo "(jsonschema not installed locally: running in a container)"; \
		$(RETRY) docker run --rm -v "$$(pwd):/work:ro" -w /work $(TOOLS_REGISTRY)/library/python:3.12-slim \
			sh -c 'pip install -q --root-user-action=ignore jsonschema && python scripts/check_fixtures.py'; \
	fi

lint: ## Shellcheck the scripts
	@if command -v shellcheck >/dev/null; then \
		shellcheck --severity=warning -x -P scripts scripts/*.sh; \
	else \
		$(RETRY) docker run --rm -v "$$(pwd):/mnt:ro" -w /mnt $(TOOLS_REGISTRY)/koalaman/shellcheck:stable --severity=warning -x -P scripts scripts/*.sh; \
	fi
	@echo "lint ok"

documents: ## Run the self-tests and validate documents against schemas/
	@rm -rf reports && mkdir -p reports
	@REPORT_DIR=reports $(MAKE) --no-print-directory smoke interop
	@echo "Documents generated in reports/"

# ── Local single-arch images (loaded into Docker; used by smoke and interop) ──

images: $(addprefix image-,$(ADAPTERS)) ## Build every adapter image for the host architecture

image-%: FORCE ## Build one adapter image, e.g. image-epri
	@echo "Building $* image ($(VERSION))..."
	@$(RETRY) docker build \
		--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
		--tag $(call image,$*):$(VERSION) \
		--file adapters/$*/Dockerfile \
		.

# ── Self-tests (images must be built) ─────────────────────────────────────────

smoke: $(addprefix smoke-,$(ADAPTERS)) ## Container-contract smoke test of every adapter

smoke-%: FORCE ## Smoke test one adapter, e.g. smoke-epri
	@scripts/smoke.sh $*

interop: ## Cross-stack self-test: every client against every server
	@scripts/interop.sh $(ADAPTERS)

# ── Dependency locks ──────────────────────────────────────────────────────────

UV_VERSION ?= 0.13.5

lock-py20305: ## Regenerate adapters/py20305/requirements.txt (hashed) from requirements.in
	@$(RETRY) docker run --rm -v "$$(pwd)/adapters/py20305:/w" -w /w $(BASE_REGISTRY)/python:3.13-slim-trixie \
		sh -c 'pip install -q --root-user-action=ignore --disable-pip-version-check uv==$(UV_VERSION) && \
		       uv pip compile -q --universal --python-version 3.13 --generate-hashes --no-header \
		       --output-file requirements.txt requirements.in'
	@echo "adapters/py20305/requirements.txt updated"

lock-envoy: ## Regenerate adapters/envoy/build-requirements.txt (hashed); Envoy's own uv.lock pins the rest
	@$(RETRY) docker run --rm -v "$$(pwd)/adapters/envoy:/w" -w /w $(BASE_REGISTRY)/python:3.13-slim-trixie \
		sh -c 'pip install -q --root-user-action=ignore --disable-pip-version-check uv==$(UV_VERSION) && \
		       uv pip compile -q --universal --python-version 3.13 --generate-hashes --no-header \
		       --output-file build-requirements.txt build-requirements.in'
	@echo "adapters/envoy/build-requirements.txt updated"

# ── Upstream candidates ───────────────────────────────────────────────────────

CANDIDATE_VERSION ?=
CANDIDATE_SHA     ?=

candidate-%: FORCE ## Build one adapter on CANDIDATE_VERSION and self-test it
	@test -n "$(CANDIDATE_VERSION)" || { echo "Error: CANDIDATE_VERSION=<upstream version> is required"; exit 1; }
	@echo "Building $* candidate on $(CANDIDATE_VERSION) $(CANDIDATE_SHA)..."
	@$(RETRY) docker build \
		--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=candidate \
		--build-arg UPSTREAM_VERSION=$(CANDIDATE_VERSION) $(if $(CANDIDATE_SHA),--build-arg UPSTREAM_SHA=$(CANDIDATE_SHA)) \
		--tag $(call image,$*):candidate \
		--file adapters/$*/Dockerfile .
	@for a in $(filter-out $*,$(ADAPTERS)); do docker tag $(call image,$$a):$(VERSION) $(call image,$$a):candidate || exit 1; done
	@VERSION=candidate scripts/smoke.sh $*
	@VERSION=candidate scripts/interop.sh $(ADAPTERS)

capabilities: ## Print the capability document of every adapter
	@for a in $(ADAPTERS); do docker run --rm $(call image,$$a):$(VERSION) print-capabilities; done

# ── Run a reference server on the host ────────────────────────────────────────

RUN_PORT ?= 8443

pki: ## Write a throwaway PKI to ./tls for compose.yaml and run-% (server names: the compose services, localhost, 127.0.0.1)
	@rm -rf tls
	@bash -c 'source scripts/lib.sh && make_pki tls "$$(jq -r .device.serial fixtures/baseline/fixture.json)" gridappsd-server envoy-server localhost 127.0.0.1' \
		&& echo "./tls: server/ (with devices/), client/, unprovisioned/, other-ca/, untrusted/"

run-%: FORCE ## Run one reference server on 127.0.0.1:$(RUN_PORT) with ./tls (make pki first), e.g. run-gridappsd
	@test -d tls || { echo "Run make pki first"; exit 1; }
	docker run --rm -p 127.0.0.1:$(RUN_PORT):443 -v "$$(pwd)/tls:/tls:ro" $(call image,$*):$(VERSION) server --version 2018 --tls-dir /tls/server

# ── Multi-arch build and publish ──────────────────────────────────────────────

buildx-setup: ## Prepare multi-arch builds: QEMU handlers and a docker-container builder
	@echo "Installing QEMU handlers and creating builder $(BUILDER)..."
	@$(RETRY) docker run --privileged --rm $(TOOLS_REGISTRY)/tonistiigi/binfmt:latest --install arm64,amd64 >/dev/null
	@docker buildx rm $(BUILDER) >/dev/null 2>&1 || true
	@$(RETRY) docker buildx create --name $(BUILDER) --driver docker-container \
		--driver-opt image=$(TOOLS_REGISTRY)/moby/buildkit:buildx-stable-1 --bootstrap >/dev/null
	@docker buildx inspect $(BUILDER) | grep -E '^(Name|Driver|Platforms)' || true

builder_flag = $(shell docker buildx inspect $(BUILDER) >/dev/null 2>&1 && echo --builder $(BUILDER))

buildx: ## Build every image for $(PLATFORMS) under emulation, without pushing
	@for a in $(ADAPTERS); do \
		echo "Building $$a for $(PLATFORMS)..."; \
		$(RETRY) docker buildx build $(builder_flag) --platform $(PLATFORMS) \
			--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
			--file adapters/$$a/Dockerfile . || exit 1; \
	done

publish: ## Build and push multi-arch images under emulation (requires VERSION=vX.Y.Z)
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must be in the form v<major>.<minor>.<patch>"; exit 1; fi
	@for a in $(ADAPTERS); do \
		echo "Publishing $$a $(VERSION) for $(PLATFORMS)..."; \
		$(RETRY) docker buildx build $(builder_flag) --platform $(PLATFORMS) --push \
			--build-arg BASE_REGISTRY=$(BASE_REGISTRY) --build-arg ADAPTER_VERSION=$(VERSION) \
			--tag $(call image,$$a):$(VERSION) \
			--file adapters/$$a/Dockerfile . || exit 1; \
	done

# ── Multi-arch publish from native runners ────────────────────────────────────

ARCH ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^aarch64$$/arm64/')
ARCHS ?= amd64 arm64

publish-arch: ## Push the local images as <image>:$(VERSION)-$(ARCH)
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must be in the form v<major>.<minor>.<patch>"; exit 1; fi
	@for a in $(ADAPTERS); do \
		got=$$(docker image inspect --format '{{.Architecture}}' $(call image,$$a):$(VERSION)) || exit 1; \
		if [ "$$got" != "$(ARCH)" ]; then echo "Error: $$a:$(VERSION) is $$got, not $(ARCH)"; exit 1; fi; \
		echo "Pushing $$a $(VERSION) for linux/$(ARCH)..."; \
		docker tag $(call image,$$a):$(VERSION) $(call image,$$a):$(VERSION)-$(ARCH) || exit 1; \
		$(RETRY) docker push --quiet $(call image,$$a):$(VERSION)-$(ARCH) || exit 1; \
	done

publish-manifest: ## Join the per-architecture images into the multi-arch <image>:$(VERSION)
	@for a in $(ADAPTERS); do \
		echo "Creating $$a $(VERSION) from: $(ARCHS)"; \
		$(RETRY) docker buildx imagetools create --tag $(call image,$$a):$(VERSION) \
			$(foreach arch,$(ARCHS),$(call image,$$a):$(VERSION)-$(arch)) || exit 1; \
		for arch in $(ARCHS); do \
			$(RETRY) docker buildx imagetools inspect $(call image,$$a):$(VERSION) --format '{{json .Manifest}}' \
				| tr -d ' \n' | grep -q "\"architecture\":\"$$arch\"" || { echo "Error: $$a:$(VERSION) has no linux/$$arch image"; exit 1; }; \
		done; \
	done

digests: ## Print "<adapter> <digest>" of the published multi-arch images of VERSION
	@for a in $(ADAPTERS); do \
		d=$$($(RETRY) docker buildx imagetools inspect $(call image,$$a):$(VERSION) --format '{{.Manifest.Digest}}') || exit 1; \
		echo "$$a $$d"; \
	done

# ── Clean ─────────────────────────────────────────────────────────────────────

clean: ## Remove test containers, networks, local images and reports
	@docker ps -aq --filter "label=ieee20305-interop.run" | xargs -r docker rm -f >/dev/null 2>&1 || true
	@docker network ls --format '{{.Name}}' | grep '^ieee20305-interop-' | xargs -r docker network rm >/dev/null 2>&1 || true
	@docker compose down --remove-orphans 2>/dev/null || true
	@for a in $(ADAPTERS); do docker rmi $(call image,$$a):$(VERSION) 2>/dev/null || true; done
	@rm -rf reports tls
