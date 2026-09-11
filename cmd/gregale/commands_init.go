// commands_init.go — Wave 0 PR-B customer-facing scaffolder for the
// stateless contract. UX spec §8 promises four reference templates
// (`s3-uploader`, `slack-bot`, `rest-api-postgres`, `cron-worker`) that
// each name the managed service to plug in and fail clearly if the
// customer forgot to `gregale secrets set` the relevant env var. This file
// is the CLI half of that promise — `docs/storage.md` is the docs half.
//
// Command shape:
//
//	gregale init --template <name> --path <dir> [--deploy] [--name <slug>] [--secrets-file <path>]
//
// `--template` is required and validated against templates.Exists (the
// authoritative list in cmd/gregale/templates/embed.go). `--path` is the
// destination directory; created if missing, refused if non-empty. The
// fail-fast contract lives in the templates themselves (each handler
// exits / 500s with the exact `gregale secrets set` hint), not here.
//
// `--deploy` chains into cmdDeployTarball with --template + --name passed
// through. `--secrets-file` is forwarded too, so cmdDeployTarball creates the
// app, seals the supplied secrets, and only then starts the first deployment.
// The customer's `--path` stays as their working copy. We never chdir — the
// chain runs in the caller's cwd, the deployment is independent.
// Service-template next steps include `deploy --create-only` so an app can be
// reserved before `secrets set` targets its slug.
//
// What this command does NOT do (UX spec §8 doesn't promise it):
//   - list templates (`--list` is implicit; customers run with a bad
//     name and see "unknown template foo (known: ...)")
//   - update templates (`gregale init` is a one-time scaffolder)
//   - set secrets from inline arguments (use a --secrets-file so values stay
//     out of shell history)
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// initCmdUsage is the top-of-failure-line shown for `gregale init` errors.
// Mirrors PrintUsage's docs URL convention (output.go:144) so the line
// carries the stable docs site pointer.
const initCmdUsage = "usage: gregale init --template <name> --path <dir> [--deploy] [--name <slug>] [--secrets-file <path>] | --list"

// initReceipt is the machine-readable result of a successful scaffolding
// operation. The path is absolute because that is what the CLI actually
// created, and because callers should not have to resolve it against a
// potentially different working directory.
type initReceipt struct {
	Template string `json:"template"`
	Path     string `json:"path"`
	Status   string `json:"status"`
	Deployed bool   `json:"deployed,omitempty"`
}

// initCmdDocsTopic identifies the CLI help topic passed to PrintUsage.
const initCmdDocsTopic = "init"

// cmdInit is the dispatcher for `gregale init`. Parses flags, validates,
// materializes, and (when --deploy) chains into cmdDeployTarball.
// Returns 0 on success, 1 on user error (per UX spec §3.2).
func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	tpl := fs.String("template", "", "template name (run with a bad value to see available names)")
	dest := fs.String("path", "", "destination directory (created if missing; refused if non-empty)")
	deploy := fs.Bool("deploy", false, "after materializing, chain into `gregale deploy --template <name> --name <slug>`")
	name := fs.String("name", "", "app slug to pass to --deploy (default: derive from --path basename)")
	secretsFile := fs.String("secrets-file", "", "read KEY=VALUE pairs and set them before the first deployment (requires --deploy)")
	list := fs.Bool("list", false, "print available templates grouped by category and exit")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, initCmdUsage, initCmdDocsTopic)
		return 1
	}
	// --list is a discovery short-circuit. It runs before --template /
	// --path validation so the customer can run `gregale init --list` on
	// its own (the most common onboarding-trail invocation).
	if *list {
		return runCmdInitList(osStdout)
	}
	if *tpl == "" {
		PrintUsage(os.Stderr, initCmdUsage+"\n  error: --template is required", initCmdDocsTopic)
		return 1
	}
	if *dest == "" {
		PrintUsage(os.Stderr, initCmdUsage+"\n  error: --path is required", initCmdDocsTopic)
		return 1
	}
	return runCmdInitWithSecrets(*tpl, *dest, *deploy, *name, *secretsFile, osStdout, os.Stderr)
}

// runCmdInitList prints the 13 templates grouped by category, in the
// canonical order pinned by templates.CategoryOrder. Each category
// gets a header (category name) + a count + a comma-separated list
// of names. This is the only place the grouping is rendered; the
// underlying classification lives in templates.CategoryFor so future
// surfaces (CLI help, dashboard template picker) can reuse it.
//
// Human output is grouped for onboarding. Under --json the dispatcher
// emits the canonical template-name slice instead, so discovery is
// scriptable without scraping category headings.
func runCmdInitList(stdout io.Writer) int {
	if jsonOutput {
		return jsonOut(writeJSONTo(stdout, struct {
			Templates []string `json:"templates"`
		}{Templates: templates.Names}))
	}
	for _, cat := range templates.CategoryOrder {
		var inCat []string
		for _, n := range templates.Names {
			if templates.CategoryFor(n) == cat {
				inCat = append(inCat, n)
			}
		}
		if len(inCat) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s (%d):\n", cat, len(inCat))
		for _, n := range inCat {
			_, _ = fmt.Fprintf(stdout, "  %s\n", n)
		}
	}
	_, _ = fmt.Fprintf(stdout, "Docs: %s\n", cliDocsURL)
	return 0
}

// runCmdInit is the pure-logic entry point. Pulled out so the test in
// commands_init_test.go can drive it directly without rebuilding the
// flag.FlagSet. osStdout / osStderr are parameters (not globals) so
// tests can pipe capture without touching the package-level seam.
//
// The function is total — every error path prints a UX-spec §3.2 line
// and returns an int exit code; no panics, no os.Exit inside.
func runCmdInit(tpl, dest string, deploy bool, name string, stdout, stderr io.Writer) int {
	return runCmdInitWithSecrets(tpl, dest, deploy, name, "", stdout, stderr)
}

// runCmdInitWithSecrets is the implementation behind runCmdInit. Keeping the
// original helper signature preserves the local scaffolder test seam while
// letting the CLI pass a secret file into the safe first-deploy path.
func runCmdInitWithSecrets(tpl, dest string, deploy bool, name, secretsFile string, stdout, stderr io.Writer) int {
	if secretsFile != "" && !deploy {
		return printErr("Invalid flags", errors.New("--secrets-file requires --deploy"))
	}
	// Step 1: validate the template name against the embedded list.
	// We use Exists (which wraps NameIsValid + Names membership) so a
	// bad flag like "--template ../../etc/passwd" is rejected before we
	// touch the embed FS.
	if !templates.Exists(tpl) {
		PrintFail(stderr, "unknown --template %q (known: %s)",
			tpl, strings.Join(templates.Names, ", "))
		return 1
	}

	// Step 2: resolve --path to an absolute path. Customers pass
	// relative paths in 95% of cases; absolute resolution here makes
	// the rest of the function simpler (no need to chdir to check
	// emptiness).
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return printErr("Could not resolve --path", err)
	}

	// Step 3: refuse non-empty destinations. Materialize over an
	// existing repo would silently overwrite customer work — that's
	// never the intent of `gregale init`. PrintFail + 1 follows the §3.2
	// user-error contract.
	if err := checkDestEmpty(absDest); err != nil {
		return printErr("Refusing to write into "+absDest, err)
	}

	// Step 4: create the destination directory. MkdirAll covers the
	// "single-level new dir" case (./uploader) and the "nested new
	// dirs" case (./tmp/foo/uploader) — the same shape mkdir(1) takes.
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return printErr("Could not create "+absDest, err)
	}

	// Step 5: materialize the embedded template into the destination.
	// templates.Materialize is os.CopyFS under the hood (embed.go:79);
	// no chdir, no intermediate tmp.
	if err := templates.Materialize(tpl, absDest); err != nil {
		return printErr("Could not write template into "+absDest, err)
	}
	if jsonOutput {
		if !deploy {
			return writeInitJSON(stdout, initReceipt{Template: tpl, Path: absDest, Status: "created"})
		}
		// The deploy composite has its own JSON receipt. Suppress that
		// nested output so `init --json --deploy` still emits exactly one
		// parseable document describing the complete operation.
		deploySlug := name
		if deploySlug == "" {
			deploySlug = sanitizeSlug(filepath.Base(absDest))
		}
		deployArgs := []string{
			"--template", tpl,
			"--name", deploySlug,
		}
		if secretsFile != "" {
			deployArgs = append(deployArgs, "--secrets-file", secretsFile)
		}
		oldOut := osStdout
		osStdout = io.Discard
		code := cmdDeployTarball(deployArgs)
		osStdout = oldOut
		if code != 0 {
			return code
		}
		return writeInitJSON(stdout, initReceipt{Template: tpl, Path: absDest, Status: "created", Deployed: true})
	}

	// Step 6: surface the customer's next steps. Per-template
	// `gregale secrets set` hints live in the template README, but we
	// always print the storage docs link so the customer lands on the
	// canonical external-storage page (UX spec §8) regardless of which
	// template they chose.
	PrintProgress(stdout, "Wrote %s template to %s", tpl, absDest)
	PrintProgress(stdout, "Next:")
	for _, line := range nextStepsFor(tpl) {
		_, _ = fmt.Fprintf(stdout, "  %s\n", line)
	}
	PrintProgress(stdout, "Docs: %s", storageDocsURL)

	// Step 7: optional deploy chain. When --deploy is set, we hand off
	// to cmdDeployTarball with --template + --name; cmdDeployTarball
	// re-materializes the template into its own tmpdir, tar.gz's it,
	// and runs the upload + log-stream flow. The customer's --path
	// stays untouched (their working copy).
	if !deploy {
		return 0
	}
	slug := name
	if slug == "" {
		slug = sanitizeSlug(filepath.Base(absDest))
	}
	PrintProgress(stdout, "Deploying %s as %s", tpl, slug)
	deployArgs := []string{
		"--template", tpl,
		"--name", slug,
	}
	if secretsFile != "" {
		deployArgs = append(deployArgs, "--secrets-file", secretsFile)
	}
	return cmdDeployTarball(deployArgs)
}

func writeInitJSON(stdout io.Writer, receipt initReceipt) int {
	if err := writeJSONTo(stdout, receipt); err != nil {
		return printErr("JSON encode failed", err)
	}
	return 0
}

// checkDestEmpty returns an error if path exists and is non-empty.
// A missing path is fine (the caller will MkdirAll); an empty
// existing path is also fine. The function is split out so the test
// can exercise the edge cases without spinning up a real FS tree.
func checkDestEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("directory is not empty (found %d entr%s); pick a fresh path or `rm -rf` first",
			len(entries), pluralY(len(entries)))
	}
	return nil
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// validateTemplateSecrets catches an incomplete first-deploy bundle before
// CreateApp runs. The handler remains the runtime source of truth, but this
// local guard turns the common missing-secret case into a no-side-effect CLI
// error. Optional template settings are intentionally not listed here.
func validateTemplateSecrets(tpl string, pairs []secretsPair) error {
	present := make(map[string]struct{}, len(pairs))
	values := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		present[pair.Key] = struct{}{}
		values[pair.Key] = pair.Value
	}
	required := map[string][]string{
		"s3-uploader":       []string{"S3_BUCKET", "S3_REGION", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"},
		"slack-bot":         []string{"SLACK_SIGNING_SECRET"},
		"rest-api-postgres": []string{"DATABASE_URL"},
		"cron-worker":       []string{"QSTASH_TOKEN", "UPSTASH_REDIS_REST_URL", "UPSTASH_REDIS_REST_TOKEN"},
	}
	var missing []string
	for _, key := range required[tpl] {
		if _, ok := present[key]; !ok || strings.TrimSpace(values[key]) == "" {
			missing = append(missing, key)
		}
	}
	if tpl == "ai-chat" {
		openAI := strings.TrimSpace(values["OPENAI_API_KEY"]) != ""
		anthropic := strings.TrimSpace(values["ANTHROPIC_API_KEY"]) != ""
		if openAI == anthropic {
			return errors.New("ai-chat requires exactly one of OPENAI_API_KEY or ANTHROPIC_API_KEY")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required secret(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// nextStepsFor returns the customer-facing "next:" lines printed after
// a successful materialization. Service templates use --secrets-file for
// their first deploy so app creation, secret sealing, and runtime startup
// happen in one supported sequence. The CLI always emits the docs link
// afterwards, so this helper omits it; see the print loop in runCmdInit.
//
// Adding a service template: append a case below AND add the matching
// --secrets-file example plus post-deploy `gregale secrets set` guidance in
// the template README so the README and CLI hint stay in lockstep.
func nextStepsFor(tpl string) []string {
	switch tpl {
	case "s3-uploader":
		return []string{
			"Create a 0600 secrets file outside this directory (one KEY=VALUE per line):",
			"  S3_BUCKET=...",
			"  S3_REGION=...",
			"  S3_ACCESS_KEY_ID=...",
			"  S3_SECRET_ACCESS_KEY=...",
			"  (optionally S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com for R2/B2)",
			"First deploy with secrets sealed before startup:",
			"  cd <dest> && gregale deploy --secrets-file <secrets-file>",
			"After the app exists, rotate/add with `gregale secrets set --app <slug> ...`.",
			"Or reserve the app before setting secrets separately:",
			"  gregale deploy --create-only --template s3-uploader --name <slug>",
			"  gregale secrets set --app <slug> S3_BUCKET=... S3_REGION=... S3_ACCESS_KEY_ID=... S3_SECRET_ACCESS_KEY=...",
			"  cd <dest> && gregale deploy",
		}
	case "slack-bot":
		return []string{
			"Create a 0600 secrets file outside this directory (one KEY=VALUE per line):",
			"  SLACK_SIGNING_SECRET=...",
			"  (optional SLACK_BOT_TOKEN=xoxb-...)",
			"First deploy with secrets sealed before startup:",
			"  cd <dest> && gregale deploy --secrets-file <secrets-file>",
			"After the app exists, rotate/add with `gregale secrets set --app <slug> ...`.",
			"Or reserve the app before setting secrets separately:",
			"  gregale deploy --create-only --template slack-bot --name <slug>",
			"  gregale secrets set --app <slug> SLACK_SIGNING_SECRET=... SLACK_BOT_TOKEN=xoxb-...",
			"  cd <dest> && gregale deploy",
		}
	case "rest-api-postgres":
		return []string{
			"Create a 0600 secrets file outside this directory (one KEY=VALUE per line):",
			"  DATABASE_URL=postgres://user:pass@host/db?sslmode=require",
			"First deploy with secrets sealed before startup:",
			"  cd <dest> && gregale deploy --secrets-file <secrets-file>",
			"After the app exists, rotate/add with `gregale secrets set --app <slug> ...`.",
			"Or reserve the app before setting secrets separately:",
			"  gregale deploy --create-only --template rest-api-postgres --name <slug>",
			"  gregale secrets set --app <slug> DATABASE_URL=postgres://user:pass@host/db?sslmode=require",
			"  cd <dest> && gregale deploy",
		}
	case "cron-worker":
		return []string{
			"Create a 0600 secrets file outside this directory (one KEY=VALUE per line):",
			"  QSTASH_TOKEN=...",
			"  UPSTASH_REDIS_REST_URL=...",
			"  UPSTASH_REDIS_REST_TOKEN=...",
			"First deploy with secrets sealed before startup:",
			"  cd <dest> && gregale deploy --secrets-file <secrets-file>",
			"Then wire QStash to invoke the function (curl or the QStash dashboard).",
			"After the app exists, rotate/add with `gregale secrets set --app <slug> ...`.",
			"Or reserve the app before setting secrets separately:",
			"  gregale deploy --create-only --template cron-worker --name <slug>",
			"  gregale secrets set --app <slug> QSTASH_TOKEN=... UPSTASH_REDIS_REST_URL=... UPSTASH_REDIS_REST_TOKEN=...",
			"  cd <dest> && gregale deploy",
		}
	case "webhook-receiver":
		return []string{
			"Create a 0600 secrets file outside this directory (one KEY=VALUE per line):",
			"  WEBHOOK_SECRET=$(openssl rand -hex 32)",
			"  (optional WEBHOOK_ALLOWED_PATHS=/stripe,/github)",
			"First deploy with secrets sealed before startup:",
			"  cd <dest> && gregale deploy --secrets-file <secrets-file>",
			"Wire the provider's webhook URL to https://<slug>.gregale.dev/<your-path>.",
			"After the app exists, rotate/add with `gregale secrets set --app <slug> ...`.",
		}
	case "ai-chat":
		return []string{
			"Create a 0600 secrets file outside this directory (set exactly one provider key):",
			"  OPENAI_API_KEY=sk-...  (or ANTHROPIC_API_KEY=sk-ant-...)",
			"  (optional OPENAI_MODEL=... and SYSTEM_PROMPT=...)",
			"First deploy with secrets sealed before startup:",
			"  cd <dest> && gregale deploy --secrets-file <secrets-file>",
			"After the app exists, rotate/add with `gregale secrets set --app <slug> ...`.",
			"Or reserve the app before setting secrets separately:",
			"  gregale deploy --create-only --template ai-chat --name <slug>",
			"  gregale secrets set --app <slug> OPENAI_API_KEY=sk-... (or ANTHROPIC_API_KEY=sk-ant-...)",
			"  cd <dest> && gregale deploy",
		}
	default:
		// The seven pre-existing templates don't need secrets; print
		// the bare deploy hint. Keeps the existing
		// `gregale deploy --template=hello-node` workflow discoverable
		// from `gregale init --template=hello-node` too.
		return []string{
			"Deploy from the new directory:",
			"  cd <dest> && gregale deploy",
		}
	}
}
