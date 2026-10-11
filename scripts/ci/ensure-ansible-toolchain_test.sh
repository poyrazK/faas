#!/usr/bin/env bash
# ensure-ansible-toolchain_test.sh — offline test for the cached CD toolchain.
# python3 and the venv it creates are stubs, so no network or pip is needed.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/ci/ensure-ansible-toolchain.sh"
command -v flock >/dev/null || { echo "SKIP: flock is not installed (it is on the Linux runners)"; exit 0; }

test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
stub_bin="$test_root/stub-bin"
mkdir -p "$stub_bin"
builds="$test_root/builds.log"

# python3 stub: `--version`, and `-m venv DIR` creating a venv whose python
# "installs" ansible-core by writing ansible-playbook/ansible-galaxy stubs.
cat >"$stub_bin/python3" <<'PY'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == --version ]]; then echo "Python 3.12.3"; exit 0; fi
[[ "$1 $2" == "-m venv" ]] || { echo "unexpected python3 $*" >&2; exit 1; }
venv="$3"
mkdir -p "$venv/bin"
echo build >>"$BUILDS_LOG"
cat >"$venv/bin/python" <<'VPY'
#!/usr/bin/env bash
set -euo pipefail
bin="$(cd "$(dirname "$0")" && pwd)"
[[ "$1 $2 $3" == "-m pip install" ]] || { echo "unexpected python $*" >&2; exit 1; }
printf '#!/usr/bin/env bash\necho "ansible-playbook [core 2.21.2]"\n' >"$bin/ansible-playbook"
cat >"$bin/ansible-galaxy" <<'GAL'
#!/usr/bin/env bash
set -euo pipefail
path=""
while [[ $# -gt 0 ]]; do
	case "$1" in --collections-path) path="$2"; shift 2 ;; *) shift ;; esac
done
mkdir -p "$path/ansible_collections/ansible/posix" "$path/ansible_collections/community/postgresql"
GAL
chmod 0755 "$bin/ansible-playbook" "$bin/ansible-galaxy"
VPY
chmod 0755 "$venv/bin/python"
PY
chmod 0755 "$stub_bin/python3"

requirements="$test_root/requirements.yml"
printf 'collections:\n  - name: ansible.posix\n    version: "2.0.0"\n  - name: community.postgresql\n    version: "4.1.0"\n' >"$requirements"

run() {
	PATH="$stub_bin:$PATH" BUILDS_LOG="$builds" RUNNER_TOOL_CACHE="$test_root/tool" RUNNER_TEMP="$test_root/tmp" \
		bash "$script" "$@"
}

first="$(run 'ansible-core==2.21.2' "$requirements")"
second="$(run 'ansible-core==2.21.2' "$requirements")"
[[ "$first" == "$second" ]] || { echo "a reused toolchain must report the same paths" >&2; exit 1; }
[[ "$(wc -l <"$builds")" -eq 1 ]] || { echo "the second job rebuilt an unchanged toolchain" >&2; exit 1; }
venv_bin="$(sed -n 's/^ANSIBLE_VENV_BIN=//p' <<<"$first")"
collections="$(sed -n 's/^ANSIBLE_COLLECTIONS_PATH=//p' <<<"$first")"
[[ -x "$venv_bin/ansible-playbook" && -d "$collections/ansible_collections/community/postgresql" ]] || {
	echo "reported toolchain paths are incomplete" >&2
	exit 1
}

# A pin change must build a separate toolchain, not reuse the old one.
third="$(run 'ansible-core==2.21.3' "$requirements")"
[[ "$third" != "$first" && "$(wc -l <"$builds")" -eq 2 ]] || { echo "a new core pin reused the old toolchain" >&2; exit 1; }

# An interrupted build (no completion marker) is discarded and rebuilt.
rm -f "$(dirname "$venv_bin")/../.complete"
run 'ansible-core==2.21.2' "$requirements" >/dev/null
[[ "$(wc -l <"$builds")" -eq 3 ]] || { echo "an incomplete toolchain was reused" >&2; exit 1; }

# Invalid pins are refused before anything is built.
if run 'ansible-core>=2.0' "$requirements" >/dev/null 2>&1; then
	echo "a non-exact core pin was accepted" >&2
	exit 1
fi
echo "ensure-ansible-toolchain: ok"
