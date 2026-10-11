// amazon-ebs.pkr.hcl — AWS builder for the Gregale Compute Image.
//
// ADR-112: composes the shared scaffolding + Ubuntu 24.04 base (same as
// hcloud.pkr.hcl). The output is an AWS AMI tagged with `local.image_tag`;
// rolling forward means a new AMI + ASG replacement. The image is
// role-agnostic — first-boot `gregalectl release install --role` does the
// role templating.
//
// Per-cloud specifics (this file):
//   - source_ami_filter: official Ubuntu 24.04 LTS AMI
//   - communicator: ssh via the EC2 ssh_keypair_name (operator-supplied)
//   - region: us-east-1 (default; override per-region)
//
// Cross-region AMI copy is out of scope (separate ADR; the image
// pipeline produces ONE AMI per region).

packer {
  required_version = ">= 1.10.0"
}

variable "aws_region" {
  type        = string
  description = "AWS region for the build + AMI. Default = us-east-1; per-region builders override."
  default     = "us-east-1"
}

variable "aws_ami_name_prefix" {
  type        = string
  description = "Prefix prepended to local.image_tag when naming the AMI. Defaults to gregale/."
  default     = "gregale/"
}

variable "aws_instance_type" {
  type        = string
  description = "EC2 instance type used during the build. t3.medium matches the EX44 vCPU count (2 vCPU, 4 GB) — large enough to compile the daemons + build the kernel."
  default     = "t3.medium"
}

variable "aws_ssh_username" {
  type        = string
  description = "Default SSH user for the Ubuntu 24.04 AMI."
  default     = "ubuntu"
}

source "amazon-ebs" "compute" {
  ami_name        = "${var.aws_ami_name_prefix}${local.image_tag}"
  instance_type   = var.aws_instance_type
  region          = var.aws_region
  ssh_username    = var.aws_ssh_username

  // Official Ubuntu 24.04 LTS AMIs — owner 099720109477 (Canonical).
  source_ami_filter {
    filters = {
      "name"                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      "root-device-type"    = "ebs"
      "virtualization-type" = "hvm"
    }
    most_recent = true
    owners      = ["099720109477"]
  }

  // Snapshot labels (AMI tags). aws_omit_artifact + aws_ami_tags differ:
  // - aws_ami_tags become Tags on the AMI itself (visible in the console).
  // - aws_snapshot_tags become Tags on the underlying EBS snapshot.
  // Both are required for inventory / cost attribution / lifecycle.
  aws_ami_tags = local.image_labels
  aws_snapshot_tags = local.image_labels

  // The AMI MUST be launchable in a VPC (HVM/EBS, not instance-store).
  // No run_tags required — the build host is short-lived.
}

build {
  name    = "amazon-ebs"
  sources = ["source.amazon-ebs.compute"]

  // Same provisioner chain as hcloud.pkr.hcl. The script files are
  // shared — the cloud-specific surface is the source block only.
  provisioner "shell" {
    script          = "scripts/install-go.sh"
    environment_vars = ["GO_VERSION=1.26.9"]
  }
  provisioner "shell" { script = "scripts/compile-daemons.sh" }
  provisioner "shell" { script = "scripts/compile-runners.sh" }
  provisioner "shell" {
    script = "scripts/prebuild-kernel.sh"
    environment_vars = ["FC_KERNEL_VERSION=${var.kernel_version}"]
  }
  provisioner "shell" {
    script = "scripts/bake-fc.sh"
    environment_vars = ["FC_RELEASE=${var.fc_release}"]
  }
  provisioner "shell" {
    script = "scripts/build-base.sh"
    environment_vars = ["FAAS_GIT_SHA=${var.git_sha}"]
  }
  provisioner "shell" { script = "scripts/verify-no-secrets.sh" }
}
