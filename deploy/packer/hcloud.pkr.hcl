// hcloud.pkr.hcl — Hetzner Cloud builder for the Gregale Compute Image.
//
// ADR-112: this file composes deploy/packer/common.pkr.hcl (3-tuple image
// identity: {fc_release, kernel_version, git_sha}) + deploy/packer/
// ubuntu-2404.pkr.hcl (Ubuntu 24.04 LTS base variables). Per-cloud builders
// MUST source the shared scaffolding and override cloud-specific source
// fields only.
//
// ADR-112: the image is role-agnostic. Role templating happens at first-boot
// via `gregalectl release install --role`. This build does NOT inject a role.
//
// Per-cloud specifics (this file):
//   - source_image: hcloud's Ubuntu 24.04 snapshot id
//   - server_type: cx22 (4 vCPU, 8 GB) — same shape as the EX44
//   - location: fsn1 (Hetzner Falkenstein; the EX44 deployment)
//   - communicator: ssh over the hcloud cloud-init ssh_pubkey
//
// The image output is a hcloud SNAPSHOT (not a server). Subsequent
// `hcloud server create --image <snapshot-id>` creates fresh nodes
// from the snapshot.

packer {
  required_version = ">= 1.10.0"
  required_plugins {
    hcloud = {
      version = ">= 1.7.0"
      source  = "github.com/hetznercloud/hcloud"
    }
  }
}

// ---------------------------------------------------------------------------
// Cloud-specific source variables. Defaults match the EX44 deployment
// (fsn1 / cx22). Override per-region via Packer CLI when needed.
// ---------------------------------------------------------------------------
variable "hcloud_server_type" {
  type        = string
  description = "Hetzner server type used during the build. Matches the EX44 deployment shape (4 vCPU, 8 GB) so the build host's perms match production's."
  default     = "cx22"
}

variable "hcloud_location" {
  type        = string
  description = "Hetzner datacenter location. fsn1 = Falkenstein (where the EX44 lives). nuernberg / ashburn / hilton are also supported."
  default     = "fsn1"
}

// Source block — uses the hcloud builder plugin to spin up a fresh
// Ubuntu 24.04 LTS server in the build location.
source "hcloud" "compute" {
  image       = "ubuntu-24.04"
  location    = var.hcloud_location
  server_type = var.hcloud_server_type
  ssh_username = "root"

  // Snapshot naming: synthesised by the common.pkr.hcl image_tag.
  snapshot_name = local.image_tag

  snapshot_labels = local.image_labels
}

// ---------------------------------------------------------------------------
// Build phase — provisioners run in this order:
//
//   1. install-go.sh       — Go 1.25.13 (matches CI go.mod pin)
//   2. compile-daemons.sh  — Go daemons + gregale + gregalectl
//   3. compile-runners.sh  — 6 function-runners (linux/amd64)
//   4. prebuild-kernel.sh  — vmlinux-6.1.134 + sha256 pin
//   5. bake-fc.sh          — firecracker-v1.7.0 + jailer + sha256
//   6. build-base.sh       — runs the explicit image-seed inventory through
//                            deploy/ansible/bootstrap.yml
//   7. verify-no-secrets.sh — fail-closed if any forbidden path present
//
// The build host is destroyed by Packer after the snapshot is taken.
// ---------------------------------------------------------------------------
build {
  name    = "hcloud"
  sources = ["source.hcloud.compute"]

  provisioner "shell" {
    script          = "scripts/install-go.sh"
    environment_vars = ["GO_VERSION=1.25.13"]
  }

  provisioner "shell" {
    script = "scripts/compile-daemons.sh"
  }

  provisioner "shell" {
    script = "scripts/compile-runners.sh"
  }

  provisioner "shell" {
    script = "scripts/prebuild-kernel.sh"
    environment_vars = [
      "FC_KERNEL_VERSION=${var.kernel_version}",
    ]
  }

  provisioner "shell" {
    script = "scripts/bake-fc.sh"
    environment_vars = [
      "FC_RELEASE=${var.fc_release}",
    ]
  }

  provisioner "shell" {
    script = "scripts/build-base.sh"
    environment_vars = [
      "FAAS_GIT_SHA=${var.git_sha}",
    ]
  }

  provisioner "shell" {
    script = "scripts/verify-no-secrets.sh"
  }
}
