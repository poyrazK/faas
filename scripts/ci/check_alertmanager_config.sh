#!/usr/bin/env bash
# Render deploy/ansible/roles/alertmanager/templates/alertmanager.yml.j2 for
# every supported delivery shape, prove amtool accepts each one, and prove
# each severity reaches the receiver the role promises. Nothing else renders
# this template before it reaches a host, so a broken route or receiver used
# to surface only as missing pages in production.
#
# Usage: check_alertmanager_config.sh [repo_root]
# AMTOOL=/path/to/amtool skips the download (local runs); otherwise the
# script fetches the Alertmanager release the role pins and verifies the
# role's SHA-256 before using it.
set -euo pipefail

repo_root=${1:-$(git rev-parse --show-toplevel)}
role="$repo_root/deploy/ansible/roles/alertmanager"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

amtool=${AMTOOL:-}
if [[ -z $amtool ]]; then
  version=$(awk -F'"' '/^am_version:/ {print $2}' "$role/defaults/main.yml")
  sha=$(awk -F'"' '/^am_release_sha256:/ {print $2}' "$role/defaults/main.yml")
  tarball="alertmanager-${version}.linux-amd64.tar.gz"
  curl --fail --silent --show-error --location --retry 3 --retry-delay 2 \
    -o "$work/$tarball" \
    "https://github.com/prometheus/alertmanager/releases/download/v${version}/${tarball}"
  echo "${sha}  $work/$tarball" | sha256sum --check --strict >/dev/null
  tar -xzf "$work/$tarball" -C "$work" "alertmanager-${version}.linux-amd64/amtool"
  amtool="$work/alertmanager-${version}.linux-amd64/amtool"
fi

# Placeholder secret files. Alertmanager reads *_file paths at send time,
# but they must be real paths for a faithful render.
for name in smtp-password pushover-token pushover-user-key discord-webhook-url slack-webhook-url discord-warn-webhook-url slack-warn-webhook-url heartbeat-url; do
  printf 'https://example.invalid/%s\n' "$name" >"$work/$name"
done

failed=0

# render <case> <extra vars as JSON>
render() {
  local name=$1 vars=$2
  printf '%s\n' "$vars" >"$work/$name.vars.json"
  ANSIBLE_LOCALHOST_WARNING=false ANSIBLE_INVENTORY_UNPARSED_WARNING=false \
    ANSIBLE_DEPRECATION_WARNINGS=false ANSIBLE_PYTHON_INTERPRETER=auto_silent \
    ansible all -i localhost, -c local \
    -e @"$role/defaults/main.yml" \
    -e @"$work/$name.vars.json" \
    -m ansible.builtin.template \
    -a "src=$role/templates/alertmanager.yml.j2 dest=$work/$name.yml mode=0600" >/dev/null
  if ! "$amtool" check-config "$work/$name.yml" >"$work/$name.check.log" 2>&1; then
    echo "alertmanager config [$name]: amtool check-config failed" >&2
    cat "$work/$name.check.log" >&2
    failed=1
  fi
}

# route <case> <expected receiver> <label=value>...
route() {
  local name=$1 want=$2
  shift 2
  if ! "$amtool" config routes test --config.file="$work/$name.yml" \
    --verify.receivers="$want" "$@" >"$work/route.log" 2>&1; then
    echo "alertmanager config [$name]: {$*} routed to '$(tr -d '\n' <"$work/route.log")', want $want" >&2
    failed=1
  fi
}

render dev '{"am_dev_mode": true}'
route dev faas-silent severity=page component=apid family=x
route dev faas-silent severity=warn component=apid family=x
route dev faas-silent alertname=FaasWatchdog severity=info component=alertmanager family=watchdog

render discord "{\"am_discord_webhook_url_file\": \"$work/discord-webhook-url\", \"am_heartbeat_url_file\": \"$work/heartbeat-url\"}"
route discord faas-page severity=page component=apid family=x
route discord faas-warn severity=warn component=apid family=x
route discord faas-silent severity=info component=apid family=x
route discord faas-heartbeat alertname=FaasWatchdog severity=info component=alertmanager family=watchdog
route discord faas-silent severity=page component=meterd family=alert_preset_signals
grep -q "api_url_file: \"$work/discord-webhook-url\"" "$work/discord.yml" || { echo "alertmanager config [discord]: Discord webhook not rendered as a Slack-compatible receiver" >&2; failed=1; }
grep -q " url_file: \"$work/heartbeat-url\"" "$work/discord.yml" || { echo "alertmanager config [discord]: heartbeat webhook not rendered" >&2; failed=1; }

render slack "{\"am_slack_webhook_url_file\": \"$work/slack-webhook-url\"}"
route slack faas-page severity=page component=apid family=x
route slack faas-warn severity=warn component=apid family=x
route slack faas-silent alertname=FaasWatchdog severity=info component=alertmanager family=watchdog
grep -q 'api_url_file:' "$work/slack.yml" || { echo "alertmanager config [slack]: no slack api_url_file rendered" >&2; failed=1; }

render email_pushover "{\"am_smtp_smarthost\": \"smtp.example.invalid:587\", \"am_smtp_password_file\": \"$work/smtp-password\", \"am_pushover_token_file\": \"$work/pushover-token\", \"am_pushover_user_key_file\": \"$work/pushover-user-key\"}"
route email_pushover faas-page severity=page component=apid family=x
route email_pushover faas-warn severity=warn component=apid family=x
route email_pushover faas-page severity=page alertname=FaasAlertmanagerNotificationsFailed
test_alert=FaasDeliveryQualification_mq_123_1_delivery_page
route email_pushover faas-silent severity=page alertname="$test_alert" qualification_id=mq-123-1

printf 'fixture-routing-key\n' >"$work/delivery-pagerduty-key"
render delivery_pagerduty "{\"am_smtp_smarthost\": \"smtp.example.invalid:587\", \"am_smtp_password_file\": \"$work/smtp-password\", \"am_heartbeat_url_file\": \"$work/heartbeat-url\", \"am_delivery_pagerduty_key_file\": \"$work/delivery-pagerduty-key\", \"am_fleet_name\": \"production\"}"
for alert in FaasPrometheusNotificationsDropped FaasAlertmanagerNotificationsFailed; do
  route delivery_pagerduty faas-delivery-page,faas-page severity=page alertname="$alert"
  route delivery_pagerduty faas-warn severity=warn alertname="$alert"
done
route delivery_pagerduty faas-page severity=page alertname=FaasSyntheticCanaryFailed
route delivery_pagerduty faas-heartbeat severity=info alertname=FaasWatchdog
route delivery_pagerduty faas-delivery-page severity=page alertname="$test_alert" qualification_id=mq-123-1

render pushover_only "{\"am_pushover_token_file\": \"$work/pushover-token\", \"am_pushover_user_key_file\": \"$work/pushover-user-key\"}"
route pushover_only faas-page severity=page component=apid family=x
route pushover_only faas-silent severity=warn component=apid family=x

# receiver_block <case> <receiver>: the rendered receiver's lines.
receiver_block() {
  awk -v want="  - name: $2" '$0 == want {on=1; print; next} on && /^  - name: / {exit} on {print}' "$work/$1.yml"
}

# ADR-1236: warn alerts are a daily review queue. With warn-only webhooks set,
# pages and warns land in different chats.
render split "{\"am_slack_webhook_url_file\": \"$work/slack-webhook-url\", \"am_slack_warn_webhook_url_file\": \"$work/slack-warn-webhook-url\", \"am_discord_webhook_url_file\": \"$work/discord-webhook-url\", \"am_discord_warn_webhook_url_file\": \"$work/discord-warn-webhook-url\", \"am_heartbeat_url_file\": \"$work/heartbeat-url\"}"
route split faas-page severity=page component=apid family=x
route split faas-warn severity=warn component=apid family=x
receiver_block split faas-warn | grep -q 'slack-warn-webhook-url' || { echo "alertmanager config [split]: faas-warn does not post to the Slack warn webhook" >&2; failed=1; }
receiver_block split faas-warn | grep -q 'discord-warn-webhook-url' || { echo "alertmanager config [split]: faas-warn does not post to the Discord warn webhook" >&2; failed=1; }
if receiver_block split faas-warn | grep -Eq '(slack|discord)-webhook-url'; then
  echo "alertmanager config [split]: faas-warn still posts to a page webhook" >&2; failed=1
fi
if receiver_block split faas-page | grep -q 'warn-webhook-url'; then
  echo "alertmanager config [split]: faas-page posts to a warn webhook" >&2; failed=1
fi
# Discord and Slack together must render one slack_configs list per receiver.
for receiver in faas-page faas-warn; do
  if [[ $(receiver_block split "$receiver" | grep -c 'slack_configs:') -ne 1 ]]; then
    echo "alertmanager config [split]: $receiver repeats slack_configs" >&2; failed=1
  fi
done
# Without warn webhooks, warn alerts fall back to the page chat.
receiver_block slack faas-warn | grep -q "$work/slack-webhook-url" || { echo "alertmanager config [slack]: faas-warn lost the page-webhook fallback" >&2; failed=1; }
# The warn route groups by family and repeats once a day.
grep -q 'group_by: \["family"\]' "$work/slack.yml" || { echo "alertmanager config [slack]: warn route does not group by family" >&2; failed=1; }
grep -q 'repeat_interval: 24h' "$work/slack.yml" || { echo "alertmanager config [slack]: warn route does not repeat daily" >&2; failed=1; }

# Evaluate the receiver templates with the expected Prometheus annotations
# for the runtime warnings in one notification.
render runtime_links "{\"am_smtp_smarthost\": \"smtp.example.invalid:587\", \"am_smtp_password_file\": \"$work/smtp-password\", \"am_slack_webhook_url_file\": \"$work/slack-webhook-url\", \"am_discord_webhook_url_file\": \"$work/discord-webhook-url\", \"am_pushover_token_file\": \"$work/pushover-token\", \"am_pushover_user_key_file\": \"$work/pushover-user-key\", \"am_grafana_base_url\": \"https://grafana.example.test/ops/\", \"am_fleet_name\": \"production-us\"}"
python3 - "$repo_root" "$work" "$amtool" <<'PY'
import html
from datetime import datetime
import json
from pathlib import Path
import subprocess
import sys

import yaml

root, work = map(Path, sys.argv[1:3])
amtool = sys.argv[3]
rules = yaml.safe_load((root / "pkg/promqlrules/testdata/runtime_collector.test.yml").read_text())
alerts = [
    {"status": "firing", "labels": dict(alert["exp_labels"], alertname=test["alertname"]),
     "annotations": alert["exp_annotations"]}
    for case in rules["tests"] for test in case["alert_rule_test"] for alert in test["exp_alerts"]
]
assert len(alerts) == 3, "expected all three runtime collector warnings in the Prometheus fixture"
alerts[0]["endsAt"] = "2026-11-18T13:00:00Z"
alerts[1].update(status="resolved", endsAt="2026-11-18T12:30:40.123456Z")
alerts[2].update(status="resolved", endsAt="2026-11-18T15:42:15.987654+03:00")
without_end = {key: value for key, value in alerts[1].items() if key != "endsAt"}
core_firing = dict(alerts[2], status="firing")
unrelated = {"status": "firing", "labels": {"alertname": "UnrelatedWarning"},
             "annotations": {"summary": "Existing warning details"}}
data = {"receiver": "faas-warn", "status": "firing", "groupLabels": {"component": "apid"},
        "externalURL": "http://alertmanager.example.test"}
glob = work / "empty.tmpl"
glob.touch()

def render(template, kind, alerts):
    path = work / "template-data.json"
    path.write_text(json.dumps(dict(data, alerts=alerts)))
    return subprocess.check_output([
        amtool, "template", "render", "--template.glob", str(glob), "--template.type", kind,
        "--template.text", template, "--template.data", str(path),
    ], text=True)

def dashboard_path(alert):
    path = alert["annotations"]["runtime_dashboard_path"]
    if alert["status"] == "resolved" and "endsAt" in alert:
        end_ms = int(datetime.fromisoformat(alert["endsAt"]).timestamp() * 1000)
        return path.removesuffix("&from=now-2h&to=now") + f"&from={end_ms - 7_200_000}&to={end_ms}"
    return path

for case, base in (("email_pushover", "http://127.0.0.1:3000"),
                   ("discord", "http://127.0.0.1:3000"),
                   ("runtime_links", "https://grafana.example.test/ops")):
    config = yaml.safe_load((work / (case + ".yml")).read_text())
    receiver = next(recv for recv in config["receivers"] if recv["name"] == "faas-warn")
    for name, field, kind in (("email_configs", "html", "html"), ("slack_configs", "text", "text")):
        for delivery in receiver.get(name, []):
            template = delivery[field]
            output = html.unescape(render(template, kind, alerts))
            for alert in alerts:
                annotations = alert["annotations"]
                for url in (base + dashboard_path(alert), annotations["runbook_url"]):
                    link = f'href="{url}"' if kind == "html" else f"<{url}>"
                    assert link in output, f"{case}/{name} missing link {url}"
                assert annotations["summary"] in output, f"{case}/{name} missing alert summary"
            for alert in (without_end, core_firing):
                rolling = html.unescape(render(template, kind, [alert]))
                url = base + dashboard_path(alert)
                link = f'href="{url}"' if kind == "html" else f"<{url}>"
                assert link in rolling, f"{case}/{name} missing rolling link {url}"
            prior = render(template, kind, [unrelated]).strip()
            if kind == "html":
                assert "Existing warning details" in prior and base not in prior, f"{case} changed unrelated email"
            else:
                assert not prior, f"{case} changed unrelated chat text"

data["receiver"] = "faas-page"
rule_groups = yaml.safe_load((root / "deploy/ansible/roles/prometheus/files/faas.rules.yml").read_text())["groups"]
page_names = ("FaasApiAvailabilityBurnRateHigh", "FaasPrometheusNotificationsDropped",
              "FaasAlertmanagerNotificationsFailed")
pages = {
    rule["alert"]: rule for group in rule_groups for rule in group.get("rules", [])
    if rule.get("alert") in page_names
}
assert set(pages) == set(page_names)
config = yaml.safe_load((work / "runtime_links.yml").read_text())
base = "https://grafana.example.test/ops"
page_receiver = next(recv for recv in config["receivers"] if recv["name"] == "faas-page")
page_alerts = []
for name in page_names:
    rule = pages[name]
    page_alerts.append({"status": "firing", "labels": dict(rule["labels"], alertname=name),
                        "annotations": dict(rule["annotations"], failure_stage="test stage")})
canary = {"status": "firing", "labels": {"alertname": "FaasSyntheticCanaryFailed"},
          "annotations": {"summary": "Canary failed", "first_action": "Open the first failing step.",
                          "failure_stage": "Deploy and invoke", "fleet": "production",
                          "active_release": "a" * 40, "last_successful_check": "Earlier verified probe",
                          "description": "Raw probe output",
                          "run_url": "https://github.com/example/faas/actions/runs/42",
                          "runbook_url": "https://github.com/example/faas/blob/main/docs/runbook.md"}}
page_alerts.append(canary)

for receiver, field, kind, default_name in (
    ("email_configs", "html", "html", "email.default.html"),
    ("slack_configs", "text", "text", "slack.default.text"),
    ("pushover_configs", "message", "text", "pushover.default.message"),
):
    for delivery in page_receiver[receiver]:
        template = delivery[field]
        for alert in page_alerts:
            annotations = alert["annotations"]
            output = html.unescape(render(template, kind, [alert]))
            first_action = f'First check: {annotations["first_action"]}'
            assert first_action in output, (receiver, alert["labels"]["alertname"], output)
            assert f'Fleet: {annotations.get("fleet", "production-us")}' in output, output
            assert f'Customer impact: {annotations.get("customer_impact", "unknown")}' in output, output
            assert f'Stage: {annotations["failure_stage"]}' in output, output
            assert f'Last successful check: {annotations.get("last_successful_check", "unknown")}' in output, output
            assert f'Active release: {annotations.get("active_release", "unknown")}' in output, output
            assert annotations["runbook_url"] in output, output
            evidence_url = annotations.get("run_url") or base + annotations["dashboard_path"]
            assert evidence_url in output, output
            assert "Raw probe output" not in output, output
            if receiver == "pushover_configs":
                url = render(delivery["url"], "text", [alert]).strip()
                assert url == evidence_url, (alert["labels"]["alertname"], url, evidence_url)

        resolved = html.unescape(render(template, kind, [dict(canary, status="resolved")]))
        assert "RESOLVED Canary failed" in resolved and "First check:" not in resolved, resolved
        assert render(template, kind, [unrelated]).strip() == render(
            '{{ template "' + default_name + '" . }}', kind, [unrelated]).strip()
        grouped = html.unescape(render(template, kind, [canary, unrelated]))
        assert "Open the first failing step." in grouped and "Existing warning details" in grouped, grouped
PY

# Secrets are referenced by path only; no placeholder URL may be inlined.
if grep -h 'example.invalid' "$work"/*.yml | grep -v -q 'smtp_smarthost'; then
  echo "alertmanager config: a secret value was inlined into the rendered config" >&2
  failed=1
fi

if [[ $failed -ne 0 ]]; then
  exit 1
fi
echo "alertmanager config: all delivery shapes parse and route as expected"
