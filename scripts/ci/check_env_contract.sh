#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec python3 - "$repo_root" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
contract_path = root / "pkg/daemonunitspec/envcontract.go"
contract = contract_path.read_text()

declared = set(re.findall(r'Name:\s*"(FAAS_[A-Z0-9_]+)"', contract))
if not declared:
    raise SystemExit("env contract check: no EnvContract entries found")

required = {}
for match in re.finditer(
    r'\{Name:\s*"(FAAS_[A-Z0-9_]+)"(?P<body>[^\n]*)\}', contract
):
    body = match.group("body")
    if "Required: true" in body:
        required[match.group(1)] = body

def strip_comments(text):
    # The contract is checked from Go string literals, so remove comments
    # before scanning to avoid treating prose examples as runtime reads.
    text = re.sub(r'/\*.*?\*/', '', text, flags=re.S)
    text = re.sub(r'//[^\n]*', '', text)
    return text

literal_re = re.compile(r'"((?:\\.|[^"\\])*)"')
reads = {}
daemon_dirs = [
    "cmd/apid", "cmd/schedd", "cmd/vmmd", "cmd/imaged", "cmd/builderd",
    "cmd/meterd", "cmd/githubd", "cmd/gatewayd-internal", "cmd/gatewayd-public",
    "cmd/vmmd-stream-bridge", "cmd/vmmd-raw-bridge",
]
for tree in [root / d for d in daemon_dirs] + [root / "pkg"]:
    for path in tree.rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        text = strip_comments(path.read_text(errors="replace"))
        for raw in literal_re.findall(text):
            value = raw
            # Only a literal that is itself a complete variable name is a
            # runtime read. Error messages and prose may mention a FAAS_*
            # name inside a longer string without reading that variable.
            if re.fullmatch(r'FAAS_[A-Z0-9_]+', value):
                reads.setdefault(value, set()).add(path.relative_to(root).as_posix())

missing = []
for name, files in sorted(reads.items()):
    if name in declared or any(prefix.endswith("_") and name.startswith(prefix) for prefix in declared):
        continue
    if name.endswith("_") and any(entry.startswith(name) for entry in declared):
        continue
    missing.append(f"{name} (read in {', '.join(sorted(files))})")
if missing:
    print("env contract check: undeclared FAAS_* literals:", file=sys.stderr)
    for item in missing:
        print(f"  {item}", file=sys.stderr)
    raise SystemExit(1)

# Required values must have a delivery path in the Ansible role for every
# owner. Shared rows are consumed on both split-box roles. DATABASE_URL is the
# deployed alias for the legacy FAAS_DATABASE_URL contract row.
role_for_daemon = {
    "apid": "control_plane_service",
    "schedd": "control_plane_service",
    "meterd": "control_plane_service",
    "githubd": "control_plane_service",
    "gatewayd-public": "control_plane_service",
    "imaged": "compute_only_service",
    "vmmd": "compute_only_service",
    "builderd": "compute_only_service",
    "gatewayd-internal": "compute_only_service",
}
role_text = {}
for role in set(role_for_daemon.values()):
    role_dir = root / "deploy/ansible/roles" / role
    role_text[role] = "\n".join(
        p.read_text(errors="replace")
        for p in role_dir.rglob("*")
        if p.is_file() and p.suffix in {".yml", ".yaml", ".j2", ".conf", ".service", ".env"}
    )

for name, body in sorted(required.items()):
    owners_match = re.search(r'Owners:\s*\[\]string\{([^}]*)\}', body)
    # The owner list may be on a prior line for a long row; recover it from
    # the full source line when needed.
    row_start = contract.rfind("{", 0, contract.find(f'Name: "{name}"'))
    row_end = contract.find("}", row_start)
    row = contract[row_start:row_end]
    owners_match = re.search(r'Owners:\s*\[\]string\{([^}]*)\}', row)
    owners = re.findall(r'"([^"]+)"', owners_match.group(1)) if owners_match else []
    for owner in owners:
        roles = [role_for_daemon[owner]] if owner in role_for_daemon else list(role_text)
        for role in roles:
            text = role_text[role]
            delivered = name in text
            if name == "FAAS_DATABASE_URL":
                delivered = delivered or "DATABASE_URL" in text
            if not delivered:
                print(
                    f"env contract check: required {name} is not delivered by Ansible role {role} (owner {owner})",
                    file=sys.stderr,
                )
                raise SystemExit(1)

print("env contract check: OK")
PY
