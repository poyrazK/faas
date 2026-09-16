#!/usr/bin/env bash
# egress-render-cross-check + egress-render-matrix shared body.
#
# This script is invoked by the Makefile (which keeps the gate names
# ergonomic for `make help`) and contains the loop bodies that
# exercise every (public_iface, masquerade_cidr, overlay_cidrs,
# masquerade_cidr_v6) input shape the Ansible host_vars can carry.
#
# src root is the first CLI arg; the Go renderer is built from
# $SRC_ROOT/cmd/faas-nft-render and the Jinja2 template is at
# $SRC_ROOT/deploy/ansible/roles/nftables/templates/policy_nftables.conf.j2.
#
# Exit 0 = every (Go, Jinja2) pair byte-identical.
# Exit 1 = at least one pair diverged (the per-row diff is emitted).

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <src-root>" >&2
  exit 2
fi

SRC_ROOT="$1"
JINJA2="$SRC_ROOT/deploy/ansible/roles/nftables/templates/policy_nftables.conf.j2"
if [[ ! -f "$JINJA2" ]]; then
  echo "egress-render: template not found at $JINJA2" >&2
  exit 2
fi

# render_go: dump the Go renderer's stdout for the given env.
# `overlay` may be a comma-separated list (matches the env var
# convention) or empty; the renderer handles both.
render_go() {
  local iface="$1"
  local cidr="$2"
  local overlay="$3"
  local v6="$4"
  local out
  out=$(cd "$SRC_ROOT" && FAAS_PUBLIC_IFACE="$iface" FAAS_MASQUERADE_CIDR="$cidr" \
        FAAS_OVERLAY_CIDRS="$overlay" FAAS_MASQUERADE_CIDR_V6="$v6" \
        go run ./cmd/faas-nft-render 2>/dev/null)
  # Normalize trailing newline so the per-row diff is whitespace-
  # insensitive (Go's pkg/netns.Render emits a final \n; the python
  # `print(..., end="")` does not).
  printf '%s\n' "$out"
}

# render_jinja: dump the Jinja2 renderer's stdout for the given env.
# Mirrors the Go renderer's input shape via the template's variable
# names. The template's `if defined` guards keep the overlay + v6
# branches quiet when their variables are empty.
render_jinja() {
  local iface="$1"
  local cidr="$2"
  local overlay="$3"
  local v6="$4"
  python3 -c "
from jinja2 import Template
overlay = '$overlay'
o_list = [c.strip() for c in overlay.split(',')] if overlay else []
print(Template(open('$JINJA2').read()).render(
    public_iface='$iface',
    masquerade_cidr='$cidr',
    overlay_cidrs=o_list,
    masquerade_cidr_v6='$v6',
), end='')
" 2>/dev/null | python3 -c "import sys; sys.stdout.write(sys.stdin.read().rstrip('\n') + '\n')"
}

# compare: print 'OK' or 'DIVERGE' for the named case.
# $1 = case label, $2 = go output, $3 = jinja output.
compare() {
  local label="$1" go_out="$2" jinja_out="$3"
  if [[ "$go_out" == "$jinja_out" ]]; then
    echo "egress-render: OK $label"
  else
    echo "egress-render: DIVERGE $label"
    diff <(echo "$go_out") <(echo "$jinja_out") || true
    return 1
  fi
}

check_cloudflare_origin_ingress() {
  local v4_file="$SRC_ROOT/deploy/ansible/roles/nftables/files/cloudflare-ips-v4.txt"
  local v6_file="$SRC_ROOT/deploy/ansible/roles/nftables/files/cloudflare-ips-v6.txt"
  local rendered
  rendered=$(python3 - "$JINJA2" "$v4_file" "$v6_file" <<'PY'
import pathlib
import sys
from jinja2 import Template

template, v4, v6 = map(pathlib.Path, sys.argv[1:])
print(Template(template.read_text()).render(
    public_iface='eth0',
    masquerade_cidr='10.100.0.0/16',
    overlay_cidrs=[],
    masquerade_cidr_v6='',
    faas_cloudflare_origin_only=True,
    faas_cloudflare_ipv4_cidrs=v4.read_text().splitlines(),
    faas_cloudflare_ipv6_cidrs=v6.read_text().splitlines(),
), end='')
PY
  )
  if grep -Fq 'tcp dport { 22,80,443 } accept' <<<"$rendered"; then
    echo "egress-render: DIVERGE cloudflare-origin retained broad public ingress"
    return 1
  fi
  while IFS= read -r cidr; do
    [[ -n "$cidr" ]] || continue
    grep -Fq "saddr $cidr tcp dport { 80,443 } accept" <<<"$rendered" || {
      echo "egress-render: DIVERGE cloudflare-origin missing $cidr"
      return 1
    }
  done < <(cat "$v4_file" "$v6_file")
  echo "egress-render: OK cloudflare-origin"
}

main() {
  local status=0
  # row format: "label|iface|cidr|overlay|v6"
  # FAAS_EGRESS_ROW_SELECTOR lets the Makefile targets pick a
  # subset vs. the full matrix:
  #   default  → full 5-row matrix (used by egress-render-matrix,
  #              the canonical input space the Ansible host_vars
  #              can carry)
  #   default-local → the day-0 single-row smoke
  #              (used by egress-render-cross-check on every push,
  #              fast enough to gate a CI run)
  local rows=()
  case "${FAAS_EGRESS_ROW_SELECTOR:-all}" in
    default-local)
      rows=("default-local|eth0|10.100.0.0/16||")
      ;;
    all)
      rows=(
        # Default-local (every box ships this on day 0)
        "default-local|eth0|10.100.0.0/16||"
        # Multi-host mesh: overlay + v6 (the shape Mega-PR-C Commit 6
        # wires through host_vars/faas-fsn-{1,2}.yml). Use a public-range
        # overlay (TEST-NET-3 203.0.113.0/24 is RFC5737 documentation; the
        # §11 deny set excludes it) so HostPolicy.Render's panic gate
        # approves the input.
        "mesh-overlay|eth0|10.100.0.0/16|203.0.113.0/24|"
        "mesh-overlay-v6|eth0|10.100.0.0/16|203.0.113.0/24|fc00::/7"
        # Hetzner compute node on a renamed NIC
        "hetzner-ens5|ens5|10.101.0.0/16||"
        # Stress: multi-CIDR overlay (the spec's "two boxes share a
        # /24" future case). Both CIDRs are public-range.
        "multi-overlay|eth0|10.100.0.0/16|203.0.113.0/25,203.0.113.128/25|fc00::/7"
      )
      ;;
    *)
      echo "unknown FAAS_EGRESS_ROW_SELECTOR: $FAAS_EGRESS_ROW_SELECTOR" >&2
      return 2
      ;;
  esac
  for row in "${rows[@]}"; do
    IFS='|' read -r label iface cidr overlay v6 <<< "$row"
    local go_out jinja_out
    go_out=$(render_go "$iface" "$cidr" "$overlay" "$v6")
    jinja_out=$(render_jinja "$iface" "$cidr" "$overlay" "$v6")
    if ! compare "$label" "$go_out" "$jinja_out"; then
      status=1
    fi
  done
  if ! check_cloudflare_origin_ingress; then
    status=1
  fi
  return $status
}

main
