#!/usr/bin/env bash
set -euo pipefail

root="${1:-.}"
dir="$root/docs/postmortems"
index="$dir/INDEX.md"
template="$dir/TEMPLATE.md"

if [[ ! -f "$index" || ! -f "$template" ]]; then
  echo "postmortems: missing $index or $template" >&2
  exit 1
fi

shopt -s nullglob
files=("$dir"/*.md)
count=0
for file in "${files[@]}"; do
  base="${file##*/}"
  [[ "$base" == "TEMPLATE.md" || "$base" == "INDEX.md" ]] && continue
  count=$((count + 1))
  for heading in Summary Impact 'Timeline (UTC)' 5-Whys 'Action Items' 'Customer Comms' Lessons; do
    if ! grep -q "^## ${heading}$" "$file"; then
      echo "postmortems: $base is missing '## ${heading}'" >&2
      exit 1
    fi
  done

  summary=$(awk '/^## Summary$/{found=1; next} /^## /{if(found) exit} found{print}' "$file" | sed '/^[[:space:]]*$/d')
  actions=$(awk '/^## Action Items$/{found=1; next} /^## /{if(found) exit} found{print}' "$file" | sed '/^[[:space:]]*$/d')
  if [[ -z "$summary" || "$summary" =~ (TBD|TODO|<!--|-->) ]]; then
    echo "postmortems: $base has an empty or placeholder Summary" >&2
    exit 1
  fi
  if [[ -z "$actions" || "$actions" =~ (TBD|TODO|<!--|-->) ]]; then
    echo "postmortems: $base has empty or placeholder Action Items" >&2
    exit 1
  fi
  if ! grep -q "${base}" "$index"; then
    echo "postmortems: $base is not linked from INDEX.md" >&2
    exit 1
  fi
done

echo "postmortems: OK (${count} completed artifact(s))"
