#!/usr/bin/env bash
# Compare the AI gateway's price catalog with LiteLLM's newest prices
# (ADR-994). A model released after pkg/outbound/aiprices/catalog.json was
# generated has no price, so the gateway refuses its calls under any cost cap
# (outbound_ai_model_unpriced) until the catalog is regenerated. The check
# regenerates the catalog, its max_output.json and max_input.json from
# LiteLLM's newest commit into a scratch directory, keeping models LiteLLM
# dropped (--keep), and reports what a regeneration would add or reprice.
#
#   check_ai_price_catalog.sh [repo root]
#
# Exits 0 when nothing would change, 1 on a difference, 2 when LiteLLM
# cannot be fetched. LITELLM_PRICES_URL and LITELLM_COMMIT override the
# source (tests point them at a local file).
set -euo pipefail

root=${1:-$(git rev-parse --show-toplevel)}
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/download" "$scratch/out"

commit=${LITELLM_COMMIT:-}
if [[ -z $commit ]]; then
  if ! commit=$(curl -fsS --max-time 20 -H 'Accept: application/vnd.github.sha' \
    https://api.github.com/repos/BerriAI/litellm/commits/main); then
    echo "ai prices: cannot resolve LiteLLM's newest commit" >&2
    exit 2
  fi
fi
url=${LITELLM_PRICES_URL:-https://raw.githubusercontent.com/BerriAI/litellm/$commit/model_prices_and_context_window.json}
if ! curl -fsSL --max-time 60 -o "$scratch/download/prices.json" "$url"; then
  echo "ai prices: cannot fetch $url" >&2
  exit 2
fi

catalog="$root/pkg/outbound/aiprices"
for name in catalog max_output max_input; do
  cp "$catalog/$name.json" "$scratch/out/$name.json"
done
generated=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["generated"])' "$catalog/catalog.json")
python3 -I "$root/scripts/gen_ai_price_catalog.py" --keep \
  --input "$scratch/download/prices.json" --commit "$commit" --generated "$generated" \
  --output "$scratch/out/catalog.json" --max-output "$scratch/out/max_output.json" \
  --max-input "$scratch/out/max_input.json"

python3 -I - "$catalog" "$scratch/out" "$commit" <<'EOF'
import json, sys

pinned_dir, fresh_dir, commit = sys.argv[1:4]
changes = []
for name in ("catalog", "max_output", "max_input"):
    pinned = json.load(open(f"{pinned_dir}/{name}.json"))["providers"]
    fresh = json.load(open(f"{fresh_dir}/{name}.json"))["providers"]
    for provider in sorted(set(pinned) | set(fresh)):
        old, new = pinned.get(provider, {}), fresh.get(provider, {})
        added = sorted(set(new) - set(old))
        changed = sorted(model for model in set(new) & set(old) if new[model] != old[model])
        if added:
            changes.append(f"{name} {provider}: {len(added)} new: {', '.join(added[:12])}{' …' if len(added) > 12 else ''}")
        if changed:
            changes.append(f"{name} {provider}: {len(changed)} changed: {', '.join(changed[:12])}{' …' if len(changed) > 12 else ''}")
if changes:
    print(f"ai prices: LiteLLM {commit} differs from the pinned catalog:")
    print("\n".join("  " + line for line in changes))
    print("regenerate with scripts/gen_ai_price_catalog.py --keep (see its docstring) and release apid and outboundd")
    sys.exit(1)
print(f"ai prices: the catalog matches LiteLLM {commit}")
EOF
