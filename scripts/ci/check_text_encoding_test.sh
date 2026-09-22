#!/usr/bin/env bash
# check_text_encoding_test.sh — fixture tests for the text-encoding gate.
#
# A gate that cannot fail is not a gate. These build throwaway trees that
# contain each defect and assert the checker rejects them, plus clean and
# near-miss trees to assert it does not cry wolf.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
checker="${here}/check_text_encoding.sh"
fails=0

make_tree() {
  local dir="$1" relpath="$2"
  mkdir -p "${dir}/$(dirname "$relpath")"
  cat > "${dir}/${relpath}"
}

expect() {
  local want="$1" name="$2" dir="$3"
  local rc=0
  bash "$checker" "$dir" >/dev/null 2>&1 || rc=$?
  if [[ "$want" == "reject" && "$rc" -eq 0 ]]; then
    echo "FAIL: ${name}: expected rejection, checker passed" >&2
    fails=$((fails + 1))
  elif [[ "$want" == "accept" && "$rc" -ne 0 ]]; then
    echo "FAIL: ${name}: expected pass, checker rejected" >&2
    bash "$checker" "$dir" 2>&1 | sed 's/^/    /' >&2
    fails=$((fails + 1))
  else
    echo "ok: ${name}"
  fi
  rm -rf "$dir"
}

# --- RULE 1: JSON built with %q is rejected. --------------------------------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/a.go" <<'EOF'
package thing

import "fmt"

func payload(reason string) string {
	return fmt.Sprintf(`{"action":"abort","reason":%q}`, reason)
}
EOF
expect reject "rule1 flags %q inside a JSON literal" "$d"

# --- RULE 1: %q outside a JSON literal is fine. -----------------------------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/b.go" <<'EOF'
package thing

import "fmt"

func describe(name string) string {
	return fmt.Sprintf("could not open %q for reading", name)
}
EOF
expect accept "rule1 ignores %q in ordinary format strings" "$d"

# --- RULE 1: test files are out of scope. -----------------------------------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/c_test.go" <<'EOF'
package thing

import "fmt"

func fixture(v string) string {
	return fmt.Sprintf(`{"field":%q}`, v)
}
EOF
expect accept "rule1 does not flag test fixtures" "$d"

# --- RULE 2: fixed-cap byte slice on a text identifier is rejected. ---------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/d.go" <<'EOF'
package thing

func trim(message string) string {
	if len(message) > 2048 {
		message = message[:2048]
	}
	return message
}
EOF
expect reject "rule2 flags a fixed-cap cut on free text" "$d"

# --- RULE 2: a *Max* constant bound is also rejected. -----------------------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/e.go" <<'EOF'
package thing

const detailMaxBytes = 512

func trim(detail string) string {
	if len(detail) > detailMaxBytes {
		detail = detail[:detailMaxBytes]
	}
	return detail
}
EOF
expect reject "rule2 flags a named-constant cap" "$d"

# --- RULE 2: cutting at a found index is safe and must not be flagged. ------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/f.go" <<'EOF'
package thing

import "strings"

func host(value string) string {
	if i := strings.IndexByte(value, ':'); i >= 0 {
		value = value[:i]
	}
	return value
}
EOF
expect accept "rule2 ignores a cut at a computed index" "$d"

# --- RULE 2: slice caps are not string truncation. --------------------------
d="$(mktemp -d)"
make_tree "$d" "pkg/thing/g.go" <<'EOF'
package thing

func page(rows []string, limit int) []string {
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}
EOF
expect accept "rule2 ignores slice pagination" "$d"

# --- The real tree must be clean. -------------------------------------------
repo_root="$(cd "${here}/../.." && pwd)"
if bash "$checker" "$repo_root" >/dev/null 2>&1; then
  echo "ok: repository is clean under both rules"
else
  echo "FAIL: repository has text-encoding violations" >&2
  bash "$checker" "$repo_root" 2>&1 | sed 's/^/    /' >&2
  fails=$((fails + 1))
fi

if (( fails > 0 )); then
  echo "check_text_encoding_test: ${fails} failure(s)" >&2
  exit 1
fi
echo "check_text_encoding_test: all passed"
