#!/usr/bin/env bash
# ensure-ansible-toolchain.sh CORE_PIN [REQUIREMENTS]
#
# Installs, or reuses, the pinned ansible-core venv plus the exact collection
# versions REQUIREMENTS (default deploy/ansible/requirements.yml) pins, and
# prints two lines for the caller to export:
#
#   ANSIBLE_VENV_BIN=<dir with ansible, ansible-playbook, ansible-galaxy>
#   ANSIBLE_COLLECTIONS_PATH=<collections root>
#
# CD jobs run on long-lived self-hosted runners, where RUNNER_TOOL_CACHE
# survives between jobs. Rebuilding the venv and re-downloading collections
# cost 25-60 s per rollout job (four or more jobs per rollout). The cache key
# covers the core pin, the python version and the requirements.yml bytes, so
# any pin change builds a fresh toolchain. A venv cannot be moved after
# creation (its scripts embed absolute paths), so it is built in place under a
# lock and marked complete only when every check passed; an interrupted build
# is discarded and rebuilt. On hosted runners the tool cache starts empty and
# this behaves like a per-job install.
set -euo pipefail

core_pin="${1:?usage: ensure-ansible-toolchain.sh 'ansible-core==X.Y.Z' [requirements.yml]}"
requirements="${2:-deploy/ansible/requirements.yml}"
[[ "$core_pin" =~ ^ansible-core==[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
	echo "::error::core pin must be ansible-core==X.Y.Z, got $core_pin" >&2
	exit 2
}
[[ -s "$requirements" ]] || { echo "::error::missing $requirements" >&2; exit 2; }

root="${RUNNER_TOOL_CACHE:-${RUNNER_TEMP:?RUNNER_TEMP or RUNNER_TOOL_CACHE must be set}}/gregale-ansible"
key="$({ printf '%s\n' "$core_pin"; python3 --version 2>&1; cat "$requirements"; } | sha256sum | cut -c1-24)"
dir="$root/$key"
mkdir -p "$root"

toolchain_works() {
	"$dir/venv/bin/ansible-playbook" --version >/dev/null 2>&1 &&
		[[ -d "$dir/collections/ansible_collections/ansible/posix" ]] &&
		[[ -d "$dir/collections/ansible_collections/community/postgresql" ]]
}

toolchain_ok() {
	[[ -f "$dir/.complete" ]] && toolchain_works
}

install_collections() {
	local collections="$1" attempt
	for attempt in 1 2 3; do
		if "$dir/venv/bin/ansible-galaxy" collection install --no-cache --timeout 30 \
			--collections-path "$collections" -r "$requirements" >&2; then
			return 0
		fi
		if [[ $attempt != 3 ]]; then
			sleep $((attempt * 5))
		fi
	done
	# Galaxy outage: clone the same pinned versions from the collections'
	# source repositories, as ci.yml does.
	echo "::warning::Ansible Galaxy unavailable; cloning the pinned collection tags" >&2
	rm -rf -- "$collections/ansible_collections"
	local pins name version
	# Captured first: set -e cannot see a failure inside < <(...).
	pins="$(awk '/^[[:space:]]*#/ {next} /- name:/ {n=$3} /version:/ {gsub(/"/, "", $2); print n, $2}' "$requirements")"
	[[ -n $pins ]]
	while read -r name version; do
		mkdir -p "$collections/ansible_collections/${name%%.*}"
		git clone --quiet --depth 1 --branch "$version" --single-branch \
			"https://github.com/ansible-collections/${name}.git" \
			"$collections/ansible_collections/${name%%.*}/${name#*.}" >&2
	done <<<"$pins"
}

exec 9>"$root/.lock"
flock 9
if toolchain_ok; then
	echo "reusing cached Ansible toolchain $key" >&2
else
	echo "building Ansible toolchain $key" >&2
	rm -rf -- "$dir"
	mkdir -p "$dir"
	python3 -m venv "$dir/venv"
	"$dir/venv/bin/python" -m pip install --quiet "$core_pin" >&2
	install_collections "$dir/collections"
	toolchain_works || { echo "::error::Ansible toolchain $key failed its post-install checks" >&2; exit 1; }
	touch "$dir/.complete"
fi
flock -u 9

printf 'ANSIBLE_VENV_BIN=%s\n' "$dir/venv/bin"
printf 'ANSIBLE_COLLECTIONS_PATH=%s\n' "$dir/collections"
