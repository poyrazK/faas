// common.pkr.hcl — shared build block consumed by every per-cloud builder.
//
// ADR-112: image tag contract
//   gregale-compute-{fc_release}-{kernel_version}-{git_sha}
//
// The previous ADR-111 4-tuple `{role, fc_release, kernel_version, git_sha}`
// collapsed the role segment out of the image (ADR-112). Role is first-boot
// metadata templated by `gregalectl release install --role`, NOT image content.
// One image binary per `{fc, kernel, sha}` triple; the operator's
// "which image do I launch?" question now has one answer per content triple
// instead of one per role.
//
// Every per-cloud builder (hcloud, amazon-ebs, iso) sources this file and
// inherits:
//   - the tag-synthesis variables (fc_release, kernel_version, git_sha)
//   - the canonical label set (image_fc_release + image_kernel_version
//     + image_git_sha + image_created_unix)
//   - the post-processor chain that emits the synthesized tag on every output
//
// Per-cloud builders MUST NOT redefine these labels; they may add cloud-specific
// labels (image_cloud, image_region, image_ami_id) after sourcing this block.
//
// `packer validate -syntax-only` MUST pass on this file even with no cloud creds:
// the build blocks in here MUST be syntactically complete but block-level source
// resolution (ami id, snapshot id) happens in the per-cloud builders.

packer {
  required_version = ">= 1.10.0"
}

// ---------------------------------------------------------------------------
// Image identity — the 3-tuple that pins every image to a specific build of
// the platform. Changing any variable in this block requires a new image tag;
// rolling forward is always "new tag + lazy re-snapshot", never an in-place
// host-side binary upgrade (ADR-005).
//
// ADR-112: role is NOT image content. The image bakes the maximal daemon
// binary set; per-daemon drop-ins (99-faas-role.conf), cgroup slices, and
// the active daemon subset are templated at first-boot by
// `gregalectl release install --role`. Every box runs the same image.
// ---------------------------------------------------------------------------
variable "fc_release" {
  type        = string
  description = "Firecracker release baked into the image. Must match deploy/ansible/roles/firecracker/defaults/main.yml:fc_release."
  default     = "v1.17.0"
}

variable "kernel_version" {
  type        = string
  description = "Linux kernel version baked into the image as vmlinux. Must match deploy/ansible/roles/firecracker/defaults/main.yml:fc_kernel_version."
  default     = "6.18.54"
}

variable "git_sha" {
  type        = string
  description = "Manifest release.git_sha (ADR-110) — every Go binary + CLI + function-runner is built from this exact commit."
  default     = "gitsha-not-set"

  validation {
    condition     = can(regex("^[a-f0-9]{7,40}$", var.git_sha))
    error_message = "Git_sha must be a 7-40 char hex git short SHA. Override only for canary builds; production must use the manifest's release.git_sha."
  }
}

// ---------------------------------------------------------------------------
// Local — values synthesised once from the variables above. Per-cloud
// builders consume these locals to drop labels, name outputs, and emit
// the canonical image tag.
// ---------------------------------------------------------------------------
locals {
  image_tag = "gregale-compute-fc${var.fc_release}-kernel${var.kernel_version}-g${var.git_sha}"

  // Strip the leading "v" from fc_release + kernel_version for compactness in
  // the tag. v1.17.0 → "1.17.0"; 6.18.54 → "6.18.54" (no leading v).
  image_tag_compact = "gregale-compute-fc${replace(var.fc_release, "v", "")}-kernel${var.kernel_version}-g${var.git_sha}"

  image_labels = {
    image_fc_release      = var.fc_release
    image_kernel_version  = var.kernel_version
    image_git_sha         = var.git_sha
    image_created_unix    = formatdate("YYYY-MM-DD'T'hh:mm:ss'Z'", timestamp())
  }
}

// ---------------------------------------------------------------------------
// Source block — the base OS. Per-cloud builders source this block (or
// override source_ami_id for cloud-specific sourcing).
//
// ubuntu-2404.pkr.hcl defines the actual source block; this file is the
// shared infrastructure ABOVE the source. Per-cloud builders compose:
//
//   source "hcloud.image" "this" {
//     // ... cloud-specific ...
//   }
//
// and then run the build phase that consumes the labels declared here.
// ---------------------------------------------------------------------------
source "null" "base" {
  communicator = "none"
}

// ---------------------------------------------------------------------------
// Build block — the SYNTHESIS ONLY. Per-cloud builders compose the actual
// build (provisioners, post-processors) into their own .pkr.hcl files.
// ---------------------------------------------------------------------------
build {
  name    = "labels-only"
  sources = ["source.null.base"]

  // Provisional no-op provisioner. Packer validate -syntax-only requires a
  // build block to contain at least one provisioner (or run on a real
  // builder source), so we declare a shell-local echo that writes nothing.
  // Per-cloud builders REPLACE this in their own .pkr.hcl via `dynamic` or
  // by sourcing common.pkr.hcl.
  provisioner "shell-local" {
    inline = ["echo common.pkr.hcl labels-only build; noop=true"]
  }
}
