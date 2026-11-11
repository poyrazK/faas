#!/usr/bin/env bash
# Exercise gcp-compute-edge-address.sh against a fake gcloud
# (ADR-20261102081618 step 2c). Without APPLY=1 nothing is written; with it,
# only what is missing is created, and delete removes it in reverse order.
# entry (ADR-20261103163954 §2) needs the forwarding rule, creates the
# 443-only direct-entry rule once and tags the node; unentry untags it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ADDR="$SCRIPT_DIR/gcp-compute-edge-address.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

# The fake gcloud logs each call. State lives in $MOCK_DIR: tags (one tag per
# line), address, target, rule and entry_rule (present when the file exists).
cat >"$ROOT/bin/gcloud" <<'MOCK'
#!/usr/bin/env bash
args=()
for a in "$@"; do
  case "$a" in --project=*|--account=*|--quiet|--verbosity=*) ;; *) args+=("$a") ;; esac
done
echo "${args[*]}" >>"$MOCK_DIR/log"
case "${args[*]}" in
  "compute instances list --filter=name=(cn-1) "*) echo us-east1-c ;;
  "compute instances list "*) ;;
  "compute instances describe cn-1 --zone us-east1-c --format=value(networkInterfaces"*) echo node-vpc ;;
  "compute instances describe cn-1 "*) paste -sd';' "$MOCK_DIR/tags" 2>/dev/null || true ;;
  "compute addresses list "*) [[ -f "$MOCK_DIR/address" ]] && cat "$MOCK_DIR/address" || true ;;
  "compute target-instances list "*) [[ -f "$MOCK_DIR/target" ]] && echo cn-1-edge || true ;;
  "compute forwarding-rules list "*) [[ -f "$MOCK_DIR/rule" ]] && echo cn-1-edge-https || true ;;
  "compute firewall-rules list "*) [[ -f "$MOCK_DIR/entry_rule" ]] && echo gregale-direct-entry-v4 || true ;;
  "compute firewall-rules create "*) touch "$MOCK_DIR/entry_rule" ;;
  "compute instances add-tags "*) echo "${args[${#args[@]}-1]}" >>"$MOCK_DIR/tags" ;;
  "compute instances remove-tags "*) grep -vx "${args[${#args[@]}-1]}" "$MOCK_DIR/tags" >"$MOCK_DIR/tags.new" || true; mv "$MOCK_DIR/tags.new" "$MOCK_DIR/tags" ;;
  "compute addresses create "*) echo 203.0.113.7 >"$MOCK_DIR/address" ;;
  "compute addresses delete "*) rm -f "$MOCK_DIR/address" ;;
  "compute target-instances create "*) touch "$MOCK_DIR/target" ;;
  "compute target-instances delete "*) rm -f "$MOCK_DIR/target" ;;
  "compute forwarding-rules create "*) touch "$MOCK_DIR/rule" ;;
  "compute forwarding-rules delete "*) rm -f "$MOCK_DIR/rule" ;;
  *) echo "unexpected gcloud call: ${args[*]}" >&2; exit 22 ;;
esac
MOCK
chmod +x "$ROOT/bin/gcloud"

export PATH="$ROOT/bin:$PATH" MOCK_DIR="$ROOT/mock" GCP_PROJECT=test-project
mkdir -p "$MOCK_DIR"
echo other-tag >"$MOCK_DIR/tags"
writes() { grep -cE ' (add-tags|remove-tags|create|delete) ' "$MOCK_DIR/log" || true; }
fail() { echo "gcp-compute-edge-address_test: $*" >&2; exit 1; }

out=$(bash "$ADDR" plan cn-1)
[[ "$out" == "cn-1 zone=us-east1-c origin_tag=no entry_tag=no address= target_instance=no forwarding_rule=no" ]] || fail "plan printed: $out"
if bash "$ADDR" entry cn-1 2>/dev/null; then fail "entry was accepted before apply"; fi
out=$(bash "$ADDR" apply cn-1)
[[ "$(writes)" == 0 ]] || fail "apply without APPLY=1 wrote"
grep -q "would: forward cn-1-edge tcp/443 -> cn-1" <<<"$out" || fail "dry apply printed: $out"

: >"$MOCK_DIR/log"
out=$(APPLY=1 bash "$ADDR" apply cn-1)
[[ "$(writes)" == 4 ]] || fail "apply made $(writes) writes, want 4"
grep -q -- "--target-instance cn-1-edge --target-instance-zone us-east1-c" "$MOCK_DIR/log" || fail "forwarding rule not aimed at the target instance"
grep -q -- "--ports 443" "$MOCK_DIR/log" || fail "forwarding rule not limited to 443"
grep -q "cn-1 public_ipv4=203.0.113.7" <<<"$out" || fail "apply did not print the address: $out"

: >"$MOCK_DIR/log"
APPLY=1 bash "$ADDR" apply cn-1 >/dev/null
[[ "$(writes)" == 0 ]] || fail "a second apply wrote $(writes) times"

out=$(bash "$ADDR" entry cn-1)
grep -q "would: firewall gregale-direct-entry-v4 in node-vpc: tcp/443 from 0.0.0.0/0 to tag gregale-entry" <<<"$out" || fail "dry entry printed: $out"
out=$(NETWORK=ops-vpc bash "$ADDR" entry cn-1)
grep -q "would: firewall gregale-direct-entry-v4 in ops-vpc:" <<<"$out" || fail "NETWORK did not override the node's network: $out"
: >"$MOCK_DIR/log"
out=$(APPLY=1 bash "$ADDR" entry cn-1)
[[ "$(writes)" == 2 ]] || fail "entry made $(writes) writes, want the rule and the tag"
grep -q -- "--rules tcp:443 --source-ranges 0.0.0.0/0 --target-tags gregale-entry" "$MOCK_DIR/log" || fail "direct-entry rule is not 443-only to the entry tag"
grep -q -- "firewall-rules create gregale-direct-entry-v4 --network node-vpc " "$MOCK_DIR/log" || fail "direct-entry rule not created in the node's network"
grep -q "cn-1 entry_ipv4=203.0.113.7" <<<"$out" || fail "entry did not print the address: $out"
: >"$MOCK_DIR/log"
APPLY=1 bash "$ADDR" entry cn-1 >/dev/null
[[ "$(writes)" == 0 ]] || fail "a second entry wrote $(writes) times"
[[ "$(bash "$ADDR" plan cn-1)" == *"origin_tag=yes entry_tag=yes"* ]] || fail "plan after entry: $(bash "$ADDR" plan cn-1)"
APPLY=1 bash "$ADDR" unentry cn-1 >/dev/null
[[ "$(bash "$ADDR" plan cn-1)" == *"origin_tag=yes entry_tag=no"* ]] || fail "unentry left: $(bash "$ADDR" plan cn-1)"
APPLY=1 bash "$ADDR" entry cn-1 >/dev/null

: >"$MOCK_DIR/log"
APPLY=1 bash "$ADDR" delete cn-1 >/dev/null
[[ "$(writes)" == 5 ]] || fail "delete made $(writes) writes, want 5"
grep -n " delete \| remove-tags " "$MOCK_DIR/log" | head -1 | grep -q "remove-tags cn-1 .*gregale-entry" || fail "delete did not untag the entry first"
grep " delete \| remove-tags " "$MOCK_DIR/log" | sed -n 2p | grep -q "forwarding-rules delete" || fail "delete did not remove the forwarding rule after the entry tag"

if bash "$ADDR" plan cn-9 2>/dev/null; then fail "an unknown node was accepted"; fi
echo "gcp-compute-edge-address_test: OK"
