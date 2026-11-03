#!/usr/bin/env bash
# Deploy-configuration gates: the checks that parse, lint and render the
# Ansible roles, Prometheus rules and observability templates the hosts run.
# The nightly checks job (ci.yml) and the pull-request deploy-config workflow
# (deploy-config.yml) both call this, so a role, rule or template change that
# no longer parses, lints or renders fails its pull request. Before, only the
# nightly run checked them, and releases are cut from main between nightlies.
#
# Usage: deploy_config_gates.sh promtool-install|rules|syntax|templates
#   promtool-install  install the promtool and prometheus the prometheus
#                     role pins (its defaults: version and SHA-256)
#   rules             promtool check every checked-in rule file and example
#   syntax            --syntax-check every production playbook; needs
#                     ansible-playbook and the pinned collections
#   templates         render the control-plane Prometheus artifacts and
#                     node_exporter unit with the production inventory
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

promtool_install() {
  local defaults=deploy/ansible/roles/prometheus/defaults/main.yml version sha tarball work
  version=$(awk -F'"' '/^prom_version:/ {print $2}' "$defaults")
  sha=$(awk -F'"' '/^prom_release_sha256:/ {print $2}' "$defaults")
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$sha" =~ ^[0-9a-f]{64}$ ]] || {
    echo "deploy_config_gates: $defaults has no prom_version/prom_release_sha256 pin" >&2
    exit 1
  }
  tarball="prometheus-${version}.linux-amd64.tar.gz"
  work=$(mktemp -d)
  curl --fail --silent --show-error --location --retry 3 --retry-delay 2 \
    -o "$work/$tarball" \
    "https://github.com/prometheus/prometheus/releases/download/v${version}/${tarball}"
  echo "${sha}  $work/$tarball" | sha256sum --check --strict
  tar -xzf "$work/$tarball" -C "$work" \
    "prometheus-${version}.linux-amd64/promtool" \
    "prometheus-${version}.linux-amd64/prometheus"
  sudo mv "$work/prometheus-${version}.linux-amd64/promtool" /usr/local/bin/promtool
  sudo mv "$work/prometheus-${version}.linux-amd64/prometheus" /usr/local/bin/prometheus
  rm -rf -- "$work"
}

rules() {
  promtool check rules \
    deploy/ansible/roles/prometheus/files/faas.rules.yml \
    deploy/ansible/roles/prometheus/files/data_repair.rules.yml \
    deploy/ansible/roles/prometheus/files/database_nodes.rules.yml \
    pkg/promqlrules/data_placement.yaml \
    examples/event-acknowledgment-monitoring/preparation.rules.yml \
    examples/event-acknowledgment-monitoring/recovery.rules.yml
  promtool check config \
    examples/event-acknowledgment-monitoring/prometheus.yml \
    examples/event-acknowledgment-monitoring/prometheus-python.yml \
    examples/event-acknowledgment-monitoring/prometheus-recovery.yml \
    examples/event-acknowledgment-monitoring/prometheus-recovery-python.yml \
    examples/event-acknowledgment-monitoring/prometheus-recovery-go.yml \
    examples/event-acknowledgment-monitoring/prometheus-recovery-go-worker.yml \
    examples/event-acknowledgment-monitoring/prometheus-recovery-node-worker.yml
  python3 -m json.tool examples/event-acknowledgment-monitoring/recovery-dashboard.json >/dev/null
}

syntax() {
  cd deploy/ansible
  ansible-playbook -i localhost, -c local tests/tcp_tls_config.yml
  # The observability smoke playbook converges only in obs-stack-smoke.yml.
  ansible-playbook --syntax-check -i localhost, tests/obs_stack_smoke.yml
  # Gate-A: parse the complete-inventory safety preflight.
  ansible-playbook --syntax-check preflight.yml
  # Gate-B: syntax-check the only production bootstrap playbook
  # before an operator can run either role-specific target.
  ansible-playbook --syntax-check bootstrap.yml
  # ADR-580: the CD entrypoint that wraps the control-plane play.
  ansible-playbook --syntax-check control_plane_converge.yml
  # Gate-C: keep the connection-free scale validator parseable.
  ansible-playbook --syntax-check scale_check.yml
  # Gate-D: keep the trusted deployment-runner bootstrap parseable.
  ansible-playbook --syntax-check fleet_runner.yml
  # ADR-143: the post-activation verify playbook is production
  # tooling (`make verify-fleet`) and must stay parseable.
  ansible-playbook --syntax-check verify.yml
  # Internal mTLS renewal is a scheduled production entrypoint.
  ansible-playbook --syntax-check pki_renew.yml
  # ADR-372: the tenant egress gateway converge entrypoint.
  ansible-playbook --syntax-check tenant_egress_gateway.yml
  # ADR-1027: the database node converge entrypoint.
  ansible-playbook --syntax-check database_node.yml
  # Gate-F: the opt-in acceptance playbooks. Neither was parsed by any
  # job, so a syntax error in them would only surface when an operator
  # ran one by hand against a real host.
  ansible-playbook --syntax-check metal-h2c-acceptance.yml
  ansible-playbook --syntax-check native-acceptance-host.yml
  # Join and control-plane adoption plays are separate production
  # entrypoints and must be parsed independently; bootstrap syntax
  # checking does not cover their task bodies.
  ansible-playbook --syntax-check node_join.yml
  ansible-playbook --syntax-check node_join_control_plane.yml
}

# Syntax-check does not evaluate Jinja, so it would not catch a template
# stored in the wrong role directory or a missing HTTP-SD target for the
# split-box compute fleet.
templates() {
  : "${RUNNER_TEMP:=$(mktemp -d)}"
  cd deploy/ansible
  test ! -e roles/prometheus/files/prometheus.service
  test ! -e roles/node_exporter/files/node_exporter.service
  ansible control_plane -i inventory/hosts.ini -c local \
    -e @roles/prometheus/defaults/main.yml \
    -e ansible_hostname=ci-control-plane \
    -m ansible.builtin.template \
    -a "src=$(pwd)/roles/prometheus/templates/prometheus.yml.j2 dest=$RUNNER_TEMP/prometheus.yml"
  ansible control_plane -i inventory/hosts.ini -c local \
    -e @roles/prometheus/defaults/main.yml \
    -e ansible_hostname=ci-control-plane \
    -e prom_promtail_enabled=true \
    -m ansible.builtin.template \
    -a "src=$(pwd)/roles/prometheus/templates/prometheus.yml.j2 dest=$RUNNER_TEMP/prometheus-with-promtail.yml"
  ansible control_plane -i inventory/hosts.ini -c local \
    -e @roles/prometheus/defaults/main.yml \
    -e ansible_hostname=ci-control-plane \
    -m ansible.builtin.template \
    -a "src=$(pwd)/roles/prometheus/templates/prometheus.service.j2 dest=$RUNNER_TEMP/prometheus.service"
  grep -q 'job_name: "gatewayd-internal"' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'http://127.0.0.1:9101/v1/internal/metrics/targets' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'job_name: "vmmd"' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'http://127.0.0.1:9101/v1/internal/metrics/vmmd-targets' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'job_name: "imaged"' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'http://127.0.0.1:9101/v1/internal/metrics/imaged-targets' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'job_name: "builderd"' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'http://127.0.0.1:9101/v1/internal/metrics/builderd-targets' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'job_name: "node-compute"' "$RUNNER_TEMP/prometheus.yml"
  grep -q 'http://127.0.0.1:9101/v1/internal/metrics/node-targets' "$RUNNER_TEMP/prometheus.yml"
  if grep -q 'job_name: "promtail' "$RUNNER_TEMP/prometheus.yml"; then
    echo 'default Prometheus config scrapes Promtail without an enabled Loki transport' >&2
    exit 1
  fi
  grep -q 'job_name: "promtail"' "$RUNNER_TEMP/prometheus-with-promtail.yml"
  grep -q 'job_name: "promtail-compute"' "$RUNNER_TEMP/prometheus-with-promtail.yml"
  grep -q 'http://127.0.0.1:9101/v1/internal/metrics/promtail-targets' "$RUNNER_TEMP/prometheus-with-promtail.yml"
  grep -q 'target_limit: 1000' "$RUNNER_TEMP/prometheus.yml"
  grep -q -- '--web.listen-address=127.0.0.1:9095' "$RUNNER_TEMP/prometheus.service"
  if grep -q '{{' "$RUNNER_TEMP/prometheus.service"; then
    echo 'rendered Prometheus systemd unit contains an unresolved template placeholder' >&2
    exit 1
  fi
  ansible compute_nodes -i inventory/hosts.ini -c local \
    -e @roles/node_exporter/defaults/main.yml \
    -m ansible.builtin.template \
    -a "src=$(pwd)/roles/node_exporter/templates/node_exporter.service.j2 dest=$RUNNER_TEMP/node_exporter.service"
  grep -q -- '--web.listen-address=127.0.0.1:9100' "$RUNNER_TEMP/node_exporter.service"
  if grep -q '{{' "$RUNNER_TEMP/node_exporter.service"; then
    echo 'rendered node_exporter systemd unit contains an unresolved template placeholder' >&2
    exit 1
  fi
  grep -q -- '--cluster.listen-address=' roles/alertmanager/files/alertmanager.service

  rules_dir="$RUNNER_TEMP/prometheus-rules"
  mkdir -p "$rules_dir"
  cp roles/prometheus/files/{faas.rules.yml,bridge.rules.yml,pg_backup.rules.yml,data_repair.rules.yml,database_nodes.rules.yml} "$rules_dir/"
  cp ../../pkg/promqlrules/data_placement.yaml "$rules_dir/"
  sed "s#/etc/prometheus#$rules_dir#g" \
    "$RUNNER_TEMP/prometheus.yml" > "$RUNNER_TEMP/prometheus-checked.yml"
  promtool check config "$RUNNER_TEMP/prometheus-checked.yml"
}

case "${1:-}" in
  promtool-install) promtool_install ;;
  rules) rules ;;
  syntax) syntax ;;
  templates) templates ;;
  *)
    echo "usage: $0 promtool-install|rules|syntax|templates" >&2
    exit 2
    ;;
esac
