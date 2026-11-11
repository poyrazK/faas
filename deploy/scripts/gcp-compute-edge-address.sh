#!/usr/bin/env bash
# A GCP compute node's inbound-only public address, so Cloudflare can reach
# its edge directly (ADR-20261102081618 §2, rollout step 2c). GCP only: a
# bare-metal node serves on its own address. Run from an operator workstation:
#
#   plan NODE...    show each node's origin tag, address and forwarding rule
#   apply NODE...   tag the node with the origin tag (the VPC's Cloudflare-only
#                   80/443 rules), reserve a regional static address, and
#                   forward its TCP 443 to the node through a target instance
#   delete NODE...  remove the forwarding rule, target instance, address and
#                   tags again
#   entry NODE...   ADR-20261103163954 §2, nothing in front: tag the node with
#                   the entry tag, which the direct-entry rule admits TCP 443
#                   to from every source, and create that rule if missing.
#                   Needs apply first: the forwarded address carries 443 only,
#                   and the node's own addresses stay unreachable
#   unentry NODE... remove the entry tag; the node is Cloudflare-only again
#
# The direct-entry rule is a security policy change
# (deploy/gcp/public-beta-policy.json access.direct_entry, which
# scripts/ops/gcp_public_beta_audit.py holds it to): the owner decides when
# to run entry with APPLY=1.
#
# An external IP on the VM's interface would move all of the node's egress,
# tenant traffic included, off Cloud NAT. A forwarding rule to a target
# instance only receives: replies to its connections leave through it, and
# everything else keeps Cloud NAT. The guest agent adds the forwarded address
# to the node's local routes; check it after apply with
#   ip route show table local | grep <address>
#
# Nothing is written unless APPLY=1; without it each change is printed. Needs
# GCP_PROJECT and gcloud signed in with compute admin rights on it
# (GCLOUD_ACCOUNT picks the account). entry creates its rule in the node's own
# VPC; NETWORK overrides that.
set -euo pipefail

: "${GCP_PROJECT:?set GCP_PROJECT to the project of the compute nodes}"
action=${1:-plan}
shift || true
REGION=${GCP_REGION:-us-east1}
ORIGIN_TAG=${ORIGIN_TAG:-gregale-origin}
ENTRY_TAG=${ENTRY_TAG:-gregale-entry}
ENTRY_RULE=${ENTRY_RULE:-gregale-direct-entry-v4}
NETWORK=${NETWORK:-}
APPLY=${APPLY:-0}
if (($# == 0)); then
  echo "usage: $0 plan|apply|delete|entry|unentry NODE..." >&2
  exit 2
fi

g() {
  local account=()
  if [[ -n "${GCLOUD_ACCOUNT:-}" ]]; then account=(--account="$GCLOUD_ACCOUNT"); fi
  gcloud "${account[@]+"${account[@]}"}" --project="$GCP_PROJECT" --quiet --verbosity=error "$@"
}

# write prints a change, and makes it when APPLY=1.
write() {
  local what=$1
  shift
  if [[ "$APPLY" == 1 ]]; then
    g "$@" >/dev/null
    echo "done: $what"
  else
    echo "would: $what"
  fi
}

zone_of() {
  g compute instances list --filter="name=($1)" --format='value(zone.basename())'
}
# network_of prints the VPC of the node's first interface: the network its
# entry rule must live in. A guessed default created the rule in a network the
# node is not on (2026-11-05: prod's VPC is not "default").
network_of() {
  g compute instances describe "$1" --zone "$2" --format='value(networkInterfaces[0].network.basename())'
}
has_tag() {
  g compute instances describe "$1" --zone "$2" --format='value(tags.items)' | tr ';' '\n' | grep -x "${3:-$ORIGIN_TAG}" >/dev/null
}
entry_rule_exists() {
  [[ -n "$(g compute firewall-rules list --filter="name=($ENTRY_RULE)" --format='value(name)')" ]]
}
address_of() {
  g compute addresses list --filter="name=($1-edge) AND region:($REGION)" --format='value(address)'
}
target_exists() {
  [[ -n "$(g compute target-instances list --filter="name=($1-edge)" --format='value(name)')" ]]
}
rule_exists() {
  [[ -n "$(g compute forwarding-rules list --filter="name=($1-edge-https) AND region:($REGION)" --format='value(name)')" ]]
}

for node in "$@"; do
  zone=$(zone_of "$node")
  if [[ -z "$zone" ]]; then
    echo "$node: no such instance in $GCP_PROJECT" >&2
    exit 1
  fi
  case "$action" in
    plan)
      tag=no; has_tag "$node" "$zone" && tag=yes
      entry=no; has_tag "$node" "$zone" "$ENTRY_TAG" && entry=yes
      target=no; target_exists "$node" && target=yes
      rule=no; rule_exists "$node" && rule=yes
      echo "$node zone=$zone origin_tag=$tag entry_tag=$entry address=$(address_of "$node" || true) target_instance=$target forwarding_rule=$rule"
      ;;
    apply)
      has_tag "$node" "$zone" ||
        write "tag $node $ORIGIN_TAG" compute instances add-tags "$node" --zone "$zone" --tags "$ORIGIN_TAG"
      [[ -n "$(address_of "$node")" ]] ||
        write "reserve address $node-edge in $REGION" compute addresses create "$node-edge" --region "$REGION"
      target_exists "$node" ||
        write "target instance $node-edge -> $node" compute target-instances create "$node-edge" --instance "$node" --zone "$zone"
      rule_exists "$node" ||
        write "forward $node-edge tcp/443 -> $node" compute forwarding-rules create "$node-edge-https" \
          --region "$REGION" --address "$node-edge" --ip-protocol TCP --ports 443 \
          --target-instance "$node-edge" --target-instance-zone "$zone"
      echo "$node public_ipv4=$(address_of "$node" || true)"
      ;;
    entry)
      if ! rule_exists "$node"; then
        echo "$node: no forwarding rule $node-edge-https; run apply first" >&2
        exit 1
      fi
      network=${NETWORK:-$(network_of "$node" "$zone")}
      if [[ -z "$network" ]]; then
        echo "$node: no network on its first interface; set NETWORK" >&2
        exit 1
      fi
      entry_rule_exists ||
        write "firewall $ENTRY_RULE in $network: tcp/443 from 0.0.0.0/0 to tag $ENTRY_TAG" compute firewall-rules create "$ENTRY_RULE" \
          --network "$network" --direction INGRESS --action ALLOW --rules tcp:443 \
          --source-ranges 0.0.0.0/0 --target-tags "$ENTRY_TAG"
      has_tag "$node" "$zone" "$ENTRY_TAG" ||
        write "tag $node $ENTRY_TAG" compute instances add-tags "$node" --zone "$zone" --tags "$ENTRY_TAG"
      echo "$node entry_ipv4=$(address_of "$node" || true)"
      ;;
    unentry)
      ! has_tag "$node" "$zone" "$ENTRY_TAG" ||
        write "untag $node $ENTRY_TAG" compute instances remove-tags "$node" --zone "$zone" --tags "$ENTRY_TAG"
      ;;
    delete)
      ! has_tag "$node" "$zone" "$ENTRY_TAG" ||
        write "untag $node $ENTRY_TAG" compute instances remove-tags "$node" --zone "$zone" --tags "$ENTRY_TAG"
      ! rule_exists "$node" ||
        write "delete forwarding rule $node-edge-https" compute forwarding-rules delete "$node-edge-https" --region "$REGION"
      ! target_exists "$node" ||
        write "delete target instance $node-edge" compute target-instances delete "$node-edge" --zone "$zone"
      [[ -z "$(address_of "$node")" ]] ||
        write "release address $node-edge" compute addresses delete "$node-edge" --region "$REGION"
      ! has_tag "$node" "$zone" ||
        write "untag $node $ORIGIN_TAG" compute instances remove-tags "$node" --zone "$zone" --tags "$ORIGIN_TAG"
      ;;
    *)
      echo "usage: $0 plan|apply|delete|entry|unentry NODE..." >&2
      exit 2
      ;;
  esac
done
