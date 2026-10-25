#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
source "$root/scripts/ci/native-e2e-combined-verdict.sh"
source "$root/scripts/ci/native-e2e-verdict.sh"
work="$(mktemp -d)";trap 'rm -rf "$work"' EXIT
sha=1111111111111111111111111111111111111111
for group in source runtime; do
 directory="$work/native-e2e-$sha-$group";mkdir -p "$directory"
 printf 'sha=%s\ngroup=%s\n' "$sha" "$group" > "$directory/native-group.txt"
 : > "$directory/native-e2e.log"
done
for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do printf -- '--- PASS: %s (1.00s)\n' "$required"; done > "$work/native-e2e-$sha-source/native-e2e.log"
printf -- '--- PASS: RuntimeEvidence (1.00s)\n' > "$work/native-e2e-$sha-runtime/native-e2e.log"
native_e2e_combined_verdict "$root" "$work" "$sha" success full >/dev/null
for result in failure cancelled skipped; do
 if native_e2e_combined_verdict "$root" "$work" "$sha" "$result" full >/dev/null 2>&1;then echo "accepted $result" >&2;exit 1;fi
done
runtime="$work/native-e2e-$sha-runtime"
printf 'sha=%s\ngroup=runtime\n' 2222222222222222222222222222222222222222 > "$runtime/native-group.txt"
if native_e2e_combined_verdict "$root" "$work" "$sha" success full >/dev/null 2>&1;then echo 'accepted wrong commit' >&2;exit 1;fi
printf 'sha=%s\ngroup=source\n' "$sha" > "$runtime/native-group.txt"
if native_e2e_combined_verdict "$root" "$work" "$sha" success full >/dev/null 2>&1;then echo 'accepted wrong group' >&2;exit 1;fi
printf 'sha=%s\ngroup=runtime\n' "$sha" > "$runtime/native-group.txt"
rm "$runtime/native-e2e.log"
if native_e2e_combined_verdict "$root" "$work" "$sha" success full >/dev/null 2>&1;then echo 'accepted missing runtime log' >&2;exit 1;fi
printf -- '--- PASS: RuntimeEvidence (1.00s)\n' > "$runtime/native-e2e.log"
source_log="$work/native-e2e-$sha-source/native-e2e.log"
grep -v "${NATIVE_E2E_REQUIRED_TESTS[0]} " "$source_log" > "$work/incomplete"
mv "$work/incomplete" "$source_log"
if native_e2e_combined_verdict "$root" "$work" "$sha" success full >/dev/null 2>&1;then echo 'accepted incomplete full chain' >&2;exit 1;fi
source "$root/scripts/ci/native-e2e-phases.sh"
union="$(for group in source runtime;do native_e2e_group_phases "$group";done)"
# The financial phase runs in its own financial-system job after the platform
# groups (e2e-native.yml), so source and runtime partition every other phase.
platform_phases=()
for phase in "${NATIVE_E2E_PHASES[@]}";do [[ "$phase" == financial ]] || platform_phases+=("$phase");done
[[ "$(printf '%s\n' "$union" | sort)" == "$(printf '%s\n' "${platform_phases[@]}" | sort)" ]] || { echo 'phase groups lost/duplicated a full phase' >&2;exit 1; }
echo 'combined native evidence and group partition checks OK'

for group in promotion-core promotion-controls promotion-load promotion-admission promotion-migrations promotion-publication;do
  directory="$work/native-e2e-$sha-$group"
  mkdir -p "$directory"
  printf 'sha=%s\ngroup=%s\n' "$sha" "$group" > "$directory/native-group.txt"
  python3 - "$root" "$directory/native-e2e.log" "$group" <<'PYLOG'
import importlib.util
from pathlib import Path
import sys
spec = importlib.util.spec_from_file_location('fixture', Path(sys.argv[1]) / 'scripts/ci/promotion-qualification_test.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
Path(sys.argv[2]).write_text(fixture.log(group=sys.argv[3]))
PYLOG
done
native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only >/dev/null
[[ -s "$work/promotion-qualification.json" ]] || { echo 'promotion report missing' >&2;exit 1; }
for group in promotion-core promotion-controls promotion-load promotion-admission promotion-migrations promotion-publication;do
  directory="$work/native-e2e-$sha-$group"
  mv "$directory/native-e2e.log" "$directory/saved.log"
  if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only >/dev/null 2>&1;then echo 'accepted missing promotion group' >&2;exit 1;fi
  mv "$directory/saved.log" "$directory/native-e2e.log"
done
# Optional capability requires its own source-bound partition, while the base
# aggregate above continues to use exactly six jobs.
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true >/dev/null 2>&1;then echo 'accepted missing service graph group' >&2;exit 1;fi
for value in invalid 1;do
 if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only "$value" >/dev/null 2>&1;then echo 'accepted invalid capability selection' >&2;exit 1;fi
done
graph_directory="$work/native-e2e-$sha-promotion-service-graph"
mkdir -p "$graph_directory"
printf 'sha=%s\ngroup=promotion-service-graph\n' "$sha" > "$graph_directory/native-group.txt"
python3 - "$root" "$graph_directory/native-e2e.log" <<'PYGRAPH'
import importlib.util
from pathlib import Path
import sys
spec = importlib.util.spec_from_file_location('fixture_graph', Path(sys.argv[1]) / 'scripts/ci/promotion-service-graph-qualification_test.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
Path(sys.argv[2]).write_text(fixture.log())
PYGRAPH
native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true >/dev/null
[[ -s "$work/service-graph-qualification.json" ]] || { echo 'service graph report missing' >&2;exit 1; }
if native_e2e_combined_verdict "$root" "$work" "$sha" success full true >/dev/null 2>&1;then echo 'accepted service graph option on wrong lane' >&2;exit 1;fi
# Coordinated mode requires both supplemental groups and keeps base-only checks independent.
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true true >/dev/null 2>&1;then echo 'accepted missing coordinated group' >&2;exit 1;fi
coordinated_directory="$work/native-e2e-$sha-promotion-coordinated"
mkdir -p "$coordinated_directory"
printf 'sha=%s\ngroup=promotion-coordinated\n' "$sha" > "$coordinated_directory/native-group.txt"
python3 - "$root" "$coordinated_directory/native-e2e.log" <<'PYCOORDINATED'
import importlib.util
from pathlib import Path
import sys
spec = importlib.util.spec_from_file_location('fixture_coordinated', Path(sys.argv[1]) / 'scripts/ci/promotion-coordinated-qualification_test.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
Path(sys.argv[2]).write_text(fixture.log())
PYCOORDINATED
native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true true >/dev/null
[[ -s "$work/coordinated-qualification.json" ]] || { echo 'coordinated report missing' >&2;exit 1; }
for value in invalid 1;do
 if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true "$value" >/dev/null 2>&1;then echo 'accepted invalid coordinated selection' >&2;exit 1;fi
done
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only false true >/dev/null 2>&1;then echo 'accepted coordinated selection without dependency' >&2;exit 1;fi
printf 'sha=%s\ngroup=promotion-coordinated\n' 2222222222222222222222222222222222222222 > "$coordinated_directory/native-group.txt"
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true true >/dev/null 2>&1;then echo 'accepted wrong coordinated source' >&2;exit 1;fi
printf 'sha=%s\ngroup=promotion-coordinated\n' "$sha" > "$coordinated_directory/native-group.txt"
printf 'native promotion qualification: final_leakcheck_rc=1 group=promotion-coordinated\n' >> "$coordinated_directory/native-e2e.log"
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true true >/dev/null 2>&1;then echo 'accepted failed coordinated cleanup' >&2;exit 1;fi
native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true false >/dev/null
printf 'native promotion qualification: final_leakcheck_rc=1 group=promotion-service-graph\n' >> "$graph_directory/native-e2e.log"
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only true >/dev/null 2>&1;then echo 'accepted failed service graph cleanup' >&2;exit 1;fi
native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only false >/dev/null
printf 'native promotion qualification: final_leakcheck_rc=1 group=promotion-publication\n' >> "$directory/native-e2e.log"
if native_e2e_combined_verdict "$root" "$work" "$sha" success project-environment-promotion-only >/dev/null 2>&1;then echo 'accepted contradictory promotion leak verdict' >&2;exit 1;fi
echo 'combined promotion evidence checks OK'
