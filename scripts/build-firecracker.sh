#!/usr/bin/env bash
# Build the release-pinned, Gregale-patched Firecracker VMM once in release CI
# (ADR-20261108170000).
#
# The source is the exact upstream commit of fc_release, checked against the
# release tag, plus the ordered patch series in
# deploy/firecracker/patches/<fc_release>/. The binary is built with the Rust
# toolchain pinned by that Firecracker revision (its rust-toolchain.toml, which
# its seccomp filters are written for) as a static musl executable, then shipped
# in the signed daemon bundle as `firecracker-vmm`. Compute nodes never build it.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
defaults_file=${FIRECRACKER_DEFAULTS_FILE:-"$repo_root/deploy/ansible/roles/firecracker/defaults/main.yml"}
output=""
work_dir=""
run_unit_tests=false

usage() {
  cat >&2 <<'USAGE'
usage: build-firecracker.sh --output PATH [--work-dir DIR] [--run-unit-tests]
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      output=$2
      shift 2
      ;;
    --work-dir)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      work_dir=$2
      shift 2
      ;;
    --run-unit-tests)
      run_unit_tests=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

fail() {
  echo "build-firecracker: $*" >&2
  exit 1
}

[[ -n "$output" ]] || { usage; exit 2; }
[[ -f "$defaults_file" ]] || fail "defaults file not found: $defaults_file"

default_value() {
  local key=$1 value
  value=$(sed -n "s/^${key}:[[:space:]]*\"\([^\"]*\)\"[[:space:]]*$/\1/p" "$defaults_file" | head -n 1)
  [[ -n "$value" ]] || fail "missing quoted $key in $defaults_file"
  printf '%s' "$value"
}

fc_release=$(default_value fc_release)
fc_arch=$(default_value fc_arch)
source_repo=$(default_value fc_source_repo)
source_commit=$(default_value fc_source_commit)
[[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || fail "fc_source_commit must be a full lowercase commit id"
[[ "$fc_arch" == "x86_64" || "$fc_arch" == "aarch64" ]] || fail "unsupported fc_arch $fc_arch"

patch_dir="$repo_root/deploy/firecracker/patches/$fc_release"
[[ -d "$patch_dir" ]] || fail "no patch series for $fc_release at $patch_dir"
patches=()
while IFS= read -r patch; do
  patches+=("$patch")
done < <(find "$patch_dir" -maxdepth 1 -type f -name '[!.]*.patch' | LC_ALL=C sort)
(( ${#patches[@]} > 0 )) || fail "patch series $patch_dir is empty"

command -v git >/dev/null || fail "git is required"
command -v rustup >/dev/null || fail "rustup is required (the toolchain comes from Firecracker's rust-toolchain.toml)"
# aws-lc-sys and other C dependencies compile for the musl target.
command -v musl-gcc >/dev/null || fail "musl-gcc is required (apt-get install musl-tools)"
# Firecracker's build script compiles its seccomp filters against libseccomp.
libseccomp_found=false
for libdir in "/usr/lib/${fc_arch}-linux-gnu" /usr/lib /usr/local/lib; do
  [[ -e "$libdir/libseccomp.so" ]] && libseccomp_found=true
done
[[ "$libseccomp_found" == true ]] || fail "libseccomp is required (apt-get install libseccomp-dev)"

cleanup_dir=""
if [[ -z "$work_dir" ]]; then
  work_dir=$(mktemp -d "${TMPDIR:-/tmp}/gregale-firecracker.XXXXXX")
  cleanup_dir=$work_dir
fi
cleanup() {
  if [[ -n "$cleanup_dir" ]]; then
    rm -rf "$cleanup_dir"
  fi
}
trap cleanup EXIT

src="$work_dir/firecracker"
rm -rf "$src"
git init -q "$src"
git -C "$src" remote add origin "$source_repo"
git -C "$src" fetch -q --depth 1 origin "$source_commit"
git -C "$src" checkout -q --detach FETCH_HEAD
[[ "$(git -C "$src" rev-parse HEAD)" == "$source_commit" ]] || fail "fetched commit is not $source_commit"
# The pin must be what the upstream tag names, so a moved tag or a typo in
# fc_source_commit cannot silently build different sources.
git -C "$src" fetch -q --depth 1 origin "refs/tags/$fc_release:refs/tags/$fc_release"
tag_commit=$(git -C "$src" rev-parse "refs/tags/$fc_release^{commit}")
[[ "$tag_commit" == "$source_commit" ]] || fail "$fc_release names $tag_commit, not the pinned $source_commit"

for patch in "${patches[@]}"; do
  git -C "$src" apply --check "$patch" || fail "patch does not apply: $patch"
  git -C "$src" apply "$patch"
  echo "build-firecracker: applied $(basename "$patch")"
done

toolchain=$(sed -n 's/^channel[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$src/rust-toolchain.toml" | head -n 1)
[[ -n "$toolchain" ]] || fail "rust-toolchain.toml has no channel"
target="${fc_arch}-unknown-linux-musl"
rustup toolchain install "$toolchain" --profile minimal
rustup target add --toolchain "$toolchain" "$target"

cd "$src"
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)
export SOURCE_DATE_EPOCH
cc_var="CC_${target//-/_}"
export "$cc_var=${!cc_var:-musl-gcc}"
# musl-gcc searches only musl's own headers; C dependencies such as
# userfaultfd-sys need the kernel UAPI headers. Expose exactly those (not
# glibc's) through a private include directory.
musl_kernel_headers="$work_dir/musl-kernel-headers"
mkdir -p "$musl_kernel_headers"
for dir in linux asm-generic; do
  [[ -d "/usr/include/$dir" ]] || fail "kernel headers missing (apt-get install linux-libc-dev)"
  ln -sfn "/usr/include/$dir" "$musl_kernel_headers/$dir"
done
[[ -d "/usr/include/${fc_arch}-linux-gnu/asm" ]] || fail "kernel asm headers missing (apt-get install linux-libc-dev)"
ln -sfn "/usr/include/${fc_arch}-linux-gnu/asm" "$musl_kernel_headers/asm"
export C_INCLUDE_PATH="$musl_kernel_headers${C_INCLUDE_PATH:+:$C_INCLUDE_PATH}"
if [[ "$run_unit_tests" == true ]]; then
  # Pure validation tests only: the device tests create TAP devices and need
  # root, so they run on test-metal (deploy/firecracker/README.md).
  cargo "+$toolchain" test --locked -p vmm --lib offload_guard
fi
cargo "+$toolchain" build --locked --release --target "$target" -p firecracker

target_dir=$(cargo "+$toolchain" metadata --format-version 1 --no-deps | sed -n 's/.*"target_directory":"\([^"]*\)".*/\1/p')
[[ -n "$target_dir" ]] || fail "cannot resolve cargo target directory"
binary="$target_dir/$target/release/firecracker"
[[ -x "$binary" ]] || fail "build produced no binary at $binary"
[[ "$(head -c 4 "$binary" | od -An -tx1 | tr -d ' \n')" == "7f454c46" ]] || fail "$binary is not an ELF executable"
version=$("$binary" --version | head -n 1)
[[ "$version" == *"${fc_release}"* ]] || fail "unexpected version string: $version"

mkdir -p "$(dirname "$output")"
install -m 0755 "$binary" "$output"
echo "build-firecracker: $version"
echo "build-firecracker: sha256 $(sha256sum "$output" | awk '{print $1}') $output"
