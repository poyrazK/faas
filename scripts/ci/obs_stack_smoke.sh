#!/usr/bin/env bash
# obs_stack_smoke.sh — converge the control plane's observability stack on this
# host in production mode and check it end to end (ADR-20261023124955).
#
# The first real converge of these roles (2026-10-24, ADR §7 smoke) found four
# bugs no unit test could: a wrong Loki release pin, Loki exiting inside its
# sandbox, the cgroup collector missing systemd's nested slices, and Postgres
# logging to a file. This script keeps that class of bug from coming back.
#
# Only for a disposable Linux host with systemd and cgroup v2, such as the
# obs-stack-smoke workflow's runner: it installs services and users and writes
# /etc/faas. Steps:
#   1. the fleet CA (gregalectl pki init); the platform_logs_pki role issues
#      the platform-logs leaves from it, as on a real control plane;
#   2. a TLS S3 endpoint on 127.0.0.1:19000 (moto, pinned) whose certificate a
#      throwaway CA signs, handed to Loki through loki_s3_ca_file;
#   3. deploy/ansible/tests/obs_stack_smoke.yml: the real roles;
#   4. the checks below, each with a deadline. On failure the units' journals
#      are printed.
#
# Environment: GREGALECTL (a linux gregalectl binary, required),
# ANSIBLE_PLAYBOOK (default ansible-playbook), SMOKE_DIR.
set -euo pipefail

ROOT=$(git rev-parse --show-toplevel)
S=${SMOKE_DIR:-/var/lib/obs-stack-smoke}
GREGALECTL=${GREGALECTL:?GREGALECTL must name a linux gregalectl binary}
ANSIBLE_PLAYBOOK=${ANSIBLE_PLAYBOOK:-ansible-playbook}
MOTO_PIN='moto[server]==5.2.3'
WAKE_ID=0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b
UNITS=(prometheus alertmanager grafana-server loki alloy node_exporter)

log() { printf '\n== %s\n' "$*"; }
fail() {
	echo "::error::obs stack smoke: $*" >&2
	exit 1
}

# wait_for SECONDS WHAT COMMAND... retries COMMAND every 3 s until it succeeds.
wait_for() {
	local limit=$1 what=$2
	local deadline=$((SECONDS + limit))
	shift 2
	until "$@" >/dev/null 2>&1; do
		((SECONDS < deadline)) || fail "$what (gave up after ${limit}s)"
		sleep 3
	done
	echo "ok: $what"
}

dump() {
	local status=$?
	if ((status != 0)); then
		for u in "${UNITS[@]}" faas-cgroup-metrics faas-schedd; do
			echo "--- journal: $u"
			sudo journalctl -u "$u" -n 40 --no-pager -o cat 2>/dev/null | cut -c1-300 || true
		done
		echo "--- s3"
		tail -n 20 "$S/s3/moto.log" 2>/dev/null || true
		echo "--- sink"
		cut -c1-300 "$S/sink.log" 2>/dev/null | tail -n 10 || true
	fi
	[[ -n "${MOTO_PID:-}" ]] && kill "$MOTO_PID" 2>/dev/null
	[[ -n "${SINK_PID:-}" ]] && kill "$SINK_PID" 2>/dev/null
	exit "$status"
}
trap dump EXIT

prom() { curl -fsS --get "http://127.0.0.1:9095/api/v1/$1" --data-urlencode "${2:-x=}"; }

log "workspace $S"
sudo install -d -m 0755 -o "$(id -u)" -g "$(id -g)" "$S" "$S/s3"
sudo install -m 0755 "$GREGALECTL" /usr/local/bin/gregalectl
ADDR=$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
[[ -n "$ADDR" ]] || fail "no private IPv4 address for Loki to bind"

log "fleet CA"
sudo gregalectl pki init --root-dir /etc/faas/tls >/dev/null

log "S3 endpoint with TLS on 127.0.0.1:19000"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
	-subj /CN=obs-smoke-s3-ca -keyout "$S/s3/ca.key" -out "$S/s3/ca.crt" 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj /CN=127.0.0.1 \
	-keyout "$S/s3/server.key" -out "$S/s3/server.csr" 2>/dev/null
printf 'subjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' >"$S/s3/ext"
openssl x509 -req -in "$S/s3/server.csr" -CA "$S/s3/ca.crt" -CAkey "$S/s3/ca.key" -CAcreateserial \
	-days 1 -extfile "$S/s3/ext" -out "$S/s3/server.crt" 2>/dev/null
chmod 0644 "$S/s3/ca.crt"
python3 -m venv "$S/s3/venv"
"$S/s3/venv/bin/pip" install --quiet --disable-pip-version-check "$MOTO_PIN"
"$S/s3/venv/bin/moto_server" -H 127.0.0.1 -p 19000 -c "$S/s3/server.crt" -k "$S/s3/server.key" >"$S/s3/moto.log" 2>&1 &
MOTO_PID=$!
wait_for 60 "S3 endpoint answers over TLS" curl -fsS --cacert "$S/s3/ca.crt" https://127.0.0.1:19000/
curl -fsS --cacert "$S/s3/ca.crt" -X PUT https://127.0.0.1:19000/loki-smoke >/dev/null
sudo install -d -m 0755 /etc/faas/secrets/loki
printf 'AWS_ACCESS_KEY_ID=obs-smoke\nAWS_SECRET_ACCESS_KEY=obs-smoke\n' | sudo tee /etc/faas/secrets/loki/s3.env >/dev/null
sudo chmod 0400 /etc/faas/secrets/loki/s3.env

log "webhook sink on 127.0.0.1:18099"
: >"$S/sink.log"
python3 "$ROOT/scripts/ci/obs_stack_smoke_helpers.py" sink 18099 "$S/sink.log" &
SINK_PID=$!

log "converge (Loki on $ADDR)"
(cd "$ROOT/deploy/ansible" && "$ANSIBLE_PLAYBOOK" -i localhost, -c local tests/obs_stack_smoke.yml \
	-e "faas_obs_loki_address=$ADDR" -e "smoke_dir=$S")

log "checks"
for u in "${UNITS[@]}" faas-cgroup-metrics.timer; do
	systemctl is-active --quiet "$u" || fail "$u is not active"
done
echo "ok: ${UNITS[*]} and faas-cgroup-metrics.timer are active"

sudo systemd-run --quiet --unit=faas-schedd --property=MemoryMax=64M \
	python3 "$ROOT/scripts/ci/obs_stack_smoke_helpers.py" replay 20 "$WAKE_ID"
obs_logs_find_wake() { sudo gregalectl obs logs --wake-id "$WAKE_ID" --since 15m --limit 5 | grep -q "$WAKE_ID"; }
wait_for 120 "a journal line reaches Loki over mTLS and gregalectl obs logs finds it" obs_logs_find_wake

loki_flush() {
	sudo curl -fsS -X POST --cacert /etc/faas/tls/ca/ca.crt \
		--cert /etc/faas/tls/prometheus/loki-client.crt --key /etc/faas/tls/prometheus/loki-client.key \
		--resolve "loki.faas:3100:$ADDR" https://loki.faas:3100/flush
}
loki_flush >/dev/null || fail "Loki /flush over mTLS"
bucket_has_chunk() { curl -fsS --cacert "$S/s3/ca.crt" https://127.0.0.1:19000/loki-smoke | grep -q '<Key>gregale/'; }
wait_for 90 "Loki writes a chunk to the S3 bucket" bucket_has_chunk

targets_up() {
	prom targets "state=active" | python3 -c '
import json, sys
up = {t["labels"]["job"] for t in json.load(sys.stdin)["data"]["activeTargets"] if t["health"] == "up"}
missing = {"prometheus", "node", "loki", "promtail"} - up
sys.exit(1 if missing else 0)'
}
wait_for 120 "Prometheus scrapes itself, node_exporter, Loki (mTLS) and Alloy" targets_up

slice_metrics() {
	grep -q 'cgroup="faas-obs.slice"' /var/lib/node_exporter/textfile_collector/cgroups.prom &&
		grep -q 'cgroup="faas-obs.slice/loki.service"' /var/lib/node_exporter/textfile_collector/cgroups.prom
}
wait_for 90 "the cgroup collector writes faas-obs.slice and its services" slice_metrics

heartbeat_delivered() { grep -q '^/heartbeat .*FaasWatchdog' "$S/sink.log"; }
wait_for 180 "FaasWatchdog reaches the heartbeat receiver through Alertmanager" heartbeat_delivered

rules_evaluated() {
	prom rules | python3 -c '
import json, sys
rules = [r for g in json.load(sys.stdin)["data"]["groups"] for r in g["rules"]]
bad = [r["name"] + ": " + r.get("lastError", "") for r in rules if r.get("health") == "err"]
for b in bad:
    print(b, file=sys.stderr)
sys.exit(1 if bad or not rules else 0)'
}
sleep 20
rules_evaluated || fail "a Prometheus rule fails to evaluate (above)"
echo "ok: every Prometheus rule evaluates without error"

dropped=$(prom query 'query=sum(loki_write_dropped_entries_total{job="promtail"}) or vector(0)' |
	python3 -c 'import json, sys; print(json.load(sys.stdin)["data"]["result"][0]["value"][1])')
[[ "$dropped" == 0 ]] || fail "Alloy dropped $dropped entries"
echo "ok: Alloy dropped no entries"

GRAFANA_AUTH="admin:$(sudo cat /etc/grafana/secrets/admin_password)"
grafana() { curl -fsS -u "$GRAFANA_AUTH" "http://127.0.0.1:3000/api/$1"; }
wait_for 60 "Grafana answers" grafana health
for uid in prometheus loki; do
	grafana "datasources/uid/$uid/health" | grep -q '"status":"OK"' || fail "Grafana's $uid datasource is unhealthy: $(grafana "datasources/uid/$uid/health" || true)"
done
echo "ok: Grafana's Prometheus and Loki (mTLS) datasources are healthy"
want=$(sudo find /var/lib/grafana/dashboards -name '*.json' | wc -l)
got=$(grafana 'search?type=dash-db&limit=500' | python3 -c 'import json, sys; print(len(json.load(sys.stdin)))')
((got == want && want > 0)) || fail "Grafana loaded $got of $want provisioned dashboards"
echo "ok: Grafana loaded all $want provisioned dashboards"

for u in loki alloy prometheus; do
	restarts=$(systemctl show -p NRestarts --value "$u")
	[[ "$restarts" == 0 ]] || fail "$u restarted $restarts times"
done
echo "ok: loki, alloy and prometheus never restarted"
log "obs stack smoke passed"
