# deploy/packer/Makefile — Packer build + validation targets for the
# Gregale Compute Image (ADR-111 + ADR-112).
#
# ADR-112: image identity is the 3-tuple (fc_release, kernel_version,
# git_sha); role is first-boot metadata, NOT image content. Per-cloud
# builds below take NO `role` variable; first-boot
# `gregalectl release install --role` does the templating.
#
# Targets:
#   make image-validate     — syntax-validate every per-cloud + shared builder
#                             (works without cloud creds; CI gate)
#   make image-build-hcloud  — build + upload hcloud snapshot (requires HCLOUD_TOKEN)
#   make image-build-amazon-ebs — build + register AWS AMI (requires AWS creds)
#   make image-build-iso    — build bare-metal autoinstall ISO (no creds)
#   make image-build-canary GIT_SHA=<sha> — publish canonical triple
#                             (release.tar.gz + cosign bundle + SBoM) to
#                             oci://ghcr.io/poyrazk/faas/release:v<sha> via
#                             oras (ADR-113 PR-B). Requires cosign + syft +
#                             oras on PATH; the existing `image-validate`
#                             glob already syntax-validates this file.
#   make image-list         — list every tagged image across clouds
#   make image-test-first-boot IMAGE_TAG=<tag> ROLE=<role> — e2e: boot VM,
#                             assert user-data, assert role templating
#                             (PR #930 + ADR-112). The image bytes are
#                             identical for every ROLE — same IMAGE_TAG
#                             boots both control-plane AND compute-only.
#   make image-test-upgrade OLD=<tag> NEW=<tag> — e2e: rolling upgrade
#   make image-test-role-mutation — e2e: in-place role mutation (PR-B /
#                             ADR-112 cluster second PR)
#
# Convention: IMAGE_TAG synthesis lives in common.pkr.hcl:local.image_tag.
# Override `IMAGE_TAG` env var when a downstream need wants a custom tag.

PACKER ?= packer
PKR_DIR := $(dir $(abspath $(firstword $(MAKEFILE_LIST))))

# Packer's `validate` and `build` subcommands accept disjoint flag sets:
#   - validate accepts: -syntax-only, -except=, -only=, -machine-readable,
#     -var=, -var-file=, -no-warn-undeclared-var, -evaluate-datasources
#   - build accepts the same PLUS -color, -force, -parallel-build=N, etc.
# Conflating them (older `COMMON_FLAGS=-syntax-only -color=false`) makes
# `packer validate -color=false` reject the unknown flag in 1.10.0:
#   flag provided but not defined: -color
# (PR #929 CI fix: split per-subcommand.)
VALIDATE_FLAGS ?= -syntax-only
BUILD_FLAGS    ?= -color=false

# Pin Packer version per ADR-111 so local + CI agree byte-for-byte.
# Install via ../scripts/install-packer.sh — apt's packer is too new.
PACKER_VERSION ?= 1.10.0

.DEFAULT_GOAL := help

.PHONY: help
help: ## show this help
	@echo "Gregale Compute Image (ADR-111) — deploy/packer/Makefile"
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  %-30s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---------------------------------------------------------------------------
# Validate — purely syntactic; works without cloud creds. This is the gate
# PR #927 / PR #928 ship behind.
# ---------------------------------------------------------------------------
.PHONY: image-validate
image-validate: ## CI gate: syntax-validate every builder (works without cloud creds)
	@command -v $(PACKER) >/dev/null 2>&1 || { \
	  echo "packer not on PATH; install via scripts/install-packer.sh $(PACKER_VERSION)" >&2; \
	  exit 1; \
	}
	@for f in $(PKR_DIR)*.pkr.hcl; do \
	  echo "image-validate: $$f"; \
	  $(PACKER) validate $(VALIDATE_FLAGS) $$f || exit 1; \
	done

# ---------------------------------------------------------------------------
# Per-cloud builds. Credentials are read from the host env; these targets
# are operator-invoked, NOT CI gates. The CI canary (image-build-canary in
# .github/workflows/ci.yml) runs `image-build-hcloud-canary` on PRs as a
# `continue-on-error` PR-visibility gate and REQUIRED on merge to main.
# ---------------------------------------------------------------------------
.PHONY: image-build-hcloud
image-build-hcloud: ## Build + upload hcloud snapshot (requires HCLOUD_TOKEN)
	@test -n "$$HCLOUD_TOKEN" || { echo "HCLOUD_TOKEN required" >&2; exit 1; }
	$(PACKER) build $(BUILD_FLAGS) deploy/packer/hcloud.pkr.hcl

.PHONY: image-build-hcloud-canary
image-build-hcloud-canary: ## CI canary: build a tag-suffixed canary image
	@test -n "$$HCLOUD_TOKEN" || { echo "HCLOUD_TOKEN required" >&2; exit 1; }
	$(PACKER) build $(BUILD_FLAGS) -var git_sha=canary-$$(date +%s) deploy/packer/hcloud.pkr.hcl

.PHONY: image-build-amazon-ebs
image-build-amazon-ebs: ## Build + register AWS AMI (requires AWS creds via env)
	$(PACKER) build $(BUILD_FLAGS) deploy/packer/amazon-ebs.pkr.hcl

.PHONY: image-build-iso
image-build-iso: ## Build bare-metal autoinstall ISO (no cloud creds; output is an .iso)
	$(PACKER) build $(BUILD_FLAGS) deploy/packer/iso.pkr.hcl

# ---------------------------------------------------------------------------
# ADR-113 PR-B (canonical daemon tarball) — OCI publish canary.
# Distinct from the per-cloud image builds above: image-build-canary
# is an *artifact* builder (it uses the null builder + shell) that
# publishes the canonical triple (release.tar.gz + cosign bundle +
# SBoM) to oci://ghcr.io/poyrazk/faas/release:v<git_sha>@sha256:<digest>.
# Run from a host with cosign, syft, and oras on PATH.
# ---------------------------------------------------------------------------
.PHONY: image-build-canary
image-build-canary: ## Publish canonical triple to OCI (requires cosign, syft, oras on PATH; git_sha via env or var)
	@test -n "$(GIT_SHA)" || { echo "GIT_SHA required (40-char lowercase hex)" >&2; exit 1; }
	$(PACKER) build $(BUILD_FLAGS) -var git_sha=$(GIT_SHA) deploy/packer/image-build-canary.pkr.hcl

# ---------------------------------------------------------------------------
# Inspect / list
# ---------------------------------------------------------------------------
.PHONY: image-list
image-list: ## List every tagged image across clouds (operator readout)
	@echo "hcloud:";  hcloud image list -o columns=name,image_type,labels 2>/dev/null | grep -E 'gregale-compute-' || echo "  (hcloud CLI not on PATH or no images)"
	@echo ""
	@echo "aws:";     aws ec2 describe-images --owners self --filters "name=gregale-compute-*" --query 'Images[].[Name,ImageId,CreationDate]' --output table 2>/dev/null || echo "  (aws CLI not on PATH or no images)"

# ---------------------------------------------------------------------------
# e2e tests (delegate to cmd/e2e — added in PR #930 and PR #931).
# ---------------------------------------------------------------------------
.PHONY: image-test-first-boot
image-test-first-boot: ## e2e: boot image + assert first-boot user-data + role templating (PR #930 + ADR-112)
	@test -n "$(IMAGE_TAG)" || { echo "IMAGE_TAG required (e.g. gregale-compute-fc1.17.0-kernel6.18.54-g1234567 — note: NO role segment, ADR-112)" >&2; exit 1; }
	@test -n "$(ROLE)" || { echo "ROLE required (control-plane|compute-only) — operator supplies via cloud-init user-data, not image tag" >&2; exit 1; }
	cd $(PKR_DIR)../.. && go test -tags=e2eimage -run TestImageFirstBoot -count=1 ./cmd/e2e/...

.PHONY: image-test-upgrade
image-test-upgrade: ## e2e: drain + signal image rollout + assert per-daemon Probe gate (PR #931)
	@test -n "$(OLD)" -a -n "$(NEW)" || { echo "OLD and NEW tags required" >&2; exit 1; }
	cd $(PKR_DIR)../.. && go test -tags=e2eimage -run TestImageUpgrade -count=1 ./cmd/e2e/...

.PHONY: image-test-role-mutation
image-test-role-mutation: ## e2e: in-place role mutation control-plane → compute-only → control-plane (PR-B / ADR-112 cluster)
	@test -n "$(IMAGE_TAG)" || { echo "IMAGE_TAG required" >&2; exit 1; }
	cd $(PKR_DIR)../.. && go test -tags=e2eimage -run TestImageRoleMutation -count=1 ./cmd/e2e/...
