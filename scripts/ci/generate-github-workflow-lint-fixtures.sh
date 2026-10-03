#!/usr/bin/env bash
# Generate customer-facing workflows in .github/workflows so the following
# actionlint step validates the CLI output with the same rules as repo-owned
# workflows. The CI job removes these temporary files afterward.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

workflow_dir=.github/workflows
run_id="${GITHUB_RUN_ID:-local-$$}"
prefix="gregale-generated-check-$run_id"
test_action_sha=f1e2d3c4b5a6987654321098765432109abcdef0
test_action_ref="poyrazK/faas/.github/actions/deploy@$test_action_sha"
setup_default="$workflow_dir/$prefix-setup-default.yml"
setup_override="$workflow_dir/$prefix-setup-override.yml"
snippet_default="$workflow_dir/$prefix-snippet-default.yml"
snippet_refresh="$workflow_dir/$prefix-snippet-refresh.yml"

for path in "$setup_default" "$setup_override" "$snippet_default" "$snippet_refresh"; do
	if [[ -e "$path" ]]; then
		printf 'generated workflow fixture already exists: %s\n' "$path" >&2
		exit 1
	fi
done

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cli="$tmpdir/gregale"
go build \
	-ldflags "-X main.githubActionDefaultSHA=$test_action_sha" \
	-o "$cli" ./cmd/gregale

generate_setup() {
	local output_path="$1"
	shift
	local receipt="$tmpdir/receipt.json"
	"$cli" --json github setup actionlint-probe \
		--repo acme/actionlint-probe \
		--workflow "$output_path" \
		--force --dry-run "$@" > "$receipt"
	jq --exit-status --raw-output \
		'.workflow_content | select(type == "string" and length > 0)' \
		"$receipt" > "$output_path"
}

expect_action_ref() {
	local path="$1"
	local expected="$2"
	if ! grep -Fq "uses: $expected" "$path"; then
		printf 'generated workflow %s does not contain expected Action ref %s\n' "$path" "$expected" >&2
		exit 1
	fi
}

generate_setup "$setup_default"
expect_action_ref "$setup_default" "$test_action_ref # v0"
generate_setup "$setup_override" \
	--pinned-sha 0123456789abcdef0123456789abcdef01234567 \
	--deploy-branches staging=staging \
	--rollout safe \
	--enable-action-updates

"$cli" deploy --github --name actionlint-probe > "$snippet_default"
expect_action_ref "$snippet_default" "$test_action_ref # v0"

# Keep the deploy snippet's --pin-action path offline and deterministic while
# exercising the shared Action tag resolver.
real_git="$(command -v git)"
fake_bin="$tmpdir/fake-bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/git" <<'GIT_WRAPPER'
#!/usr/bin/env bash
if [[ "$1" == "ls-remote" ]]; then
	printf '%s\trefs/tags/v0\n' "$ACTIONLINT_PIN_SHA"
	exit 0
fi
exec "$ACTIONLINT_REAL_GIT" "$@"
GIT_WRAPPER
chmod +x "$fake_bin/git"

PATH="$fake_bin:$PATH" \
	ACTIONLINT_REAL_GIT="$real_git" \
	ACTIONLINT_PIN_SHA="$test_action_sha" \
	"$cli" deploy --github --name actionlint-probe --pin-action > "$snippet_refresh"
expect_action_ref "$snippet_refresh" "$test_action_ref # v0"
