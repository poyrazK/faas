#!/usr/bin/env bash
# scripts/e2e-journeys.sh — run the customer journey suite (tests/journeys)
# against a deployed Gregale from this machine.
#
# The suite drives the platform only through the released `gregale` CLI
# and public URLs, so it needs no KVM, Postgres or daemon builds. Every app
# it creates is named e2ej-<run>-<name> and deleted when its journey ends.
#
#   scripts/e2e-journeys.sh                       # smoke tier, prod, your CLI login
#   scripts/e2e-journeys.sh --tier full
#   scripts/e2e-journeys.sh --run 'TestReleaseAndRollback|TestScaleToZero'
#   scripts/e2e-journeys.sh --cli-version v0.1.18-rc.296
#
# See tests/journeys/README.md.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

usage() {
	cat <<'EOF'
usage: scripts/e2e-journeys.sh [flags]

  --tier smoke|full      smoke: core GA path (default); full: every journey
  --run REGEX            only journeys matching this go test -run expression
  --api URL              API origin (default https://api.gregale.dev, or $GREGALE_E2E_API)
  --apps-domain DOMAIN   app host suffix (default: the API host without "api.")
  --cli PATH             gregale binary to drive (default: download --cli-version)
  --cli-version TAG      release whose CLI to download with gh (default: newest v* release)
  --token-file PATH      read an API key from this file instead of using the CLI's
                         own `gregale login` (default); $GREGALE_E2E_TOKEN wins when set
  --out DIR              where to write go-test.json and report.md
                         (default .e2e-journeys/<timestamp>)
  --keep                 keep the apps of failed journeys for debugging
  --max-deploys N        concurrent deploys (default 4; a cache-miss build reserves a 20 GiB
                         builder drive, so lower this when sources change)
  --max-apps N           test apps held at once (default: the account's free app slots)
  --sweep-hours N        first delete e2ej- apps from runs older than N hours (default 3, 0 = off)
  --list                 list journeys and exit
EOF
}

tier=smoke
run_re=''
api="${GREGALE_E2E_API:-https://api.gregale.dev}"
apps_domain="${GREGALE_E2E_APPS_DOMAIN:-}"
cli=''
cli_version=''
token_file=''
out=''
keep=0
max_deploys=4
max_apps=''
sweep_hours=3
list=0

while (($#)); do
	case "$1" in
	--tier) tier="$2"; shift 2 ;;
	--run) run_re="$2"; shift 2 ;;
	--api) api="$2"; shift 2 ;;
	--apps-domain) apps_domain="$2"; shift 2 ;;
	--cli) cli="$2"; shift 2 ;;
	--cli-version) cli_version="$2"; shift 2 ;;
	--token-file) token_file="$2"; shift 2 ;;
	--out) out="$2"; shift 2 ;;
	--keep) keep=1; shift ;;
	--max-deploys) max_deploys="$2"; shift 2 ;;
	--max-apps) max_apps="$2"; shift 2 ;;
	--sweep-hours) sweep_hours="$2"; shift 2 ;;
	--list) list=1; shift ;;
	-h|--help) usage; exit 0 ;;
	*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
	esac
done

case "$tier" in smoke|full) ;; *) echo "--tier must be smoke or full" >&2; exit 2 ;; esac
[[ "$sweep_hours" =~ ^[0-9]+([.][0-9]+)?$ ]] || { echo "--sweep-hours must be a number" >&2; exit 2; }
command -v go >/dev/null || { echo "go is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }

if ((list)); then
	cd "$REPO_ROOT"
	go test ./tests/journeys -list '.*' | grep '^Test' | grep -v '^TestHarness'
	exit 0
fi

# resolve_cli downloads the release CLI for this OS/arch into a cache and
# verifies it against the release's CLI-SHA256SUMS.
resolve_cli() {
	local version="$1" os arch cache archive
	command -v gh >/dev/null || { echo "gh is required to download the CLI; pass --cli PATH instead" >&2; return 1; }
	if [[ -z "$version" ]]; then
		version="$(gh release list --repo poyrazK/faas --limit 20 --json tagName \
			--jq '[.[] | select(.tagName | startswith("v"))][0].tagName')"
		[[ -n "$version" ]] || { echo "could not find a v* release; pass --cli-version" >&2; return 1; }
	fi
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "unsupported arch $(uname -m)" >&2; return 1 ;; esac
	cache="${XDG_CACHE_HOME:-$HOME/.cache}/gregale-e2e/cli/${version}-${os}-${arch}"
	if [[ ! -x "$cache/gregale" ]]; then
		archive="gregale_${version#v}_${os}_${arch}.tar.gz"
		mkdir -p "$cache"
		gh release download "$version" --repo poyrazK/faas --dir "$cache" --clobber \
			--pattern "$archive" --pattern CLI-SHA256SUMS >&2
		(cd "$cache" && grep " ${archive}\$" CLI-SHA256SUMS | shasum -a 256 -c - >&2)
		tar -xzf "$cache/$archive" -C "$cache" gregale
		rm -f "$cache/$archive"
	fi
	echo "$cache/gregale"
}

if [[ -z "$cli" ]]; then
	cli="$(resolve_cli "$cli_version")"
fi
[[ -x "$cli" ]] || { echo "CLI not executable: $cli" >&2; exit 1; }

# Credentials: an explicit API key (env or file), else the CLI's own login.
# The login lives in the OS keychain, so the suite never reads it; the CLI
# authenticates itself.
token="${GREGALE_E2E_TOKEN:-}"
if [[ -z "$token" && -n "$token_file" ]]; then
	[[ -r "$token_file" ]] || { echo "cannot read --token-file $token_file" >&2; exit 1; }
	token="$(tr -d '[:space:]' <"$token_file")"
fi
auth=''
if [[ -z "$token" ]]; then
	auth="cli-login"
	if ! account="$(FAAS_API="$api" "$cli" --non-interactive whoami 2>&1)"; then
		echo "warning: the CLI is not logged in to $api ($account); only public journeys will run" >&2
		echo "         run \`$cli login\` or pass GREGALE_E2E_TOKEN / --token-file" >&2
		auth=''
	else
		echo "journeys: using your CLI login: $account" >&2
	fi
fi

if [[ -z "$out" ]]; then
	out="$REPO_ROOT/.e2e-journeys/$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "$out"

cli_reported="$("$cli" version 2>/dev/null || echo unknown)"
echo "journeys: tier=$tier api=$api cli=$cli_reported out=$out" >&2

cd "$REPO_ROOT"
set +e
GREGALE_E2E_API="$api" \
	GREGALE_E2E_APPS_DOMAIN="$apps_domain" \
	GREGALE_E2E_TOKEN="$token" \
	GREGALE_E2E_AUTH="$auth" \
	GREGALE_E2E_CLI="$cli" \
	GREGALE_E2E_TIER="$tier" \
	GREGALE_E2E_KEEP="$keep" \
	GREGALE_E2E_MAX_DEPLOYS="$max_deploys" \
	GREGALE_E2E_MAX_APPS="$max_apps" \
	GREGALE_E2E_SWEEP_HOURS="$( [[ "$sweep_hours" == 0 ]] || echo "$sweep_hours")" \
	go test ./tests/journeys -count=1 -json -timeout 90m -parallel 32 -skip '^TestHarness' ${run_re:+-run "$run_re"} 2>&1 |
	tee "$out/go-test.json" |
	python3 -I tests/journeys/report.py --markdown "$out/report.md" \
		--header "target: $api" --header "tier: $tier" --header "cli: $cli_reported" \
		--header "started: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
status=("${PIPESTATUS[@]}")
set -e

echo "report: $out/report.md" >&2
if ((status[0] != 0 || status[2] != 0)); then
	exit 1
fi
