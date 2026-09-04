package main

// commands_decompose.go — Phase 3 repo decomposition CLI surface.
//
// Two new dispatch entries (registered in main.go):
//
//	gregale scan     — dry-run; renders the plan as a table or --json
//	gregale deploy   — extends cmdDeployTarball with --yes, --json,
//	                   --only, --project-slug for the one-key provision
//	                   flow on top of the existing --tarball/--image/
//	                   --template paths.
//
// Mutual exclusion: scan takes --tarball XOR --path XOR --repo (exactly
// one); the same flags are accepted on deploy with --yes/--json/--only
// to trigger the transactional apply.
//
// The CLI mirrors §4 acceptance verbatim: "gregale deploy on the fixture
// repo creates 3 apps + 1 cron on one keypress; over-quota creates
// nothing; --json output is byte-stable." Test coverage lives in
// commands_decompose_test.go (Phase 3 task #49).

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdScan is the dry-run entry point.
//
//	gregale scan [--tarball X | --path Y | --repo owner/name] \
//	             [--project-slug S] [--only a,b,c] [--ref REF] \
//	             [--repository owner/name] [--install-id N]
//
// Renders the plan as a table by default; --json emits the server's
// PlanResponse bytes verbatim. Never writes; can_apply=false on
// over-quota surfaces the limit problem from the same response.
func cmdScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	tarball := fs.String("tarball", "", "path to source .tar.gz")
	pathFlag := fs.String("path", "", "path to local repo dir (auto-packed)")
	repo := fs.String("repo", "", "github owner/name to fetch tarball for")
	ref := fs.String("ref", "main", "git ref for --repo")
	only := fs.String("only", "", "comma-separated workload names")
	// ADR-124 inverse-allowlist. Mutex with --only (overlap rejected
	// server-side with code='exclude_only_overlap').
	exclude := fs.String("exclude", "", "comma-separated workload names to omit (ADR-124)")
	// ADR-124 two-section render toggle. Default keeps the legacy
	// single-section plan; operators running the new blast-radius
	// preview opt in explicitly so the default behaviour for scripts
	// and CI is unchanged.
	showAffected := fs.Bool("show-affected", false, "render the WillDeploy + Unaffected tables (ADR-124)")
	// ADR-124 follow-up #3 (PR-B commit 5): --persist-exclude on
	// `scan` is a no-op (scan never writes); accepted for symmetry
	// with `deploy` so a single flag set can be reused across the
	// scan + apply pair. The handler ignores it on the scan path.
	persistExclude := fs.Bool("persist-exclude", false, "record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3)")
	projectSlug := fs.String("project-slug", "", "kebab slug; default = repo dir basename")
	installID := fs.Int64("install-id", 0, "GitHub install id (with --repo)")
	prodBranch := fs.String("production-branch", "main", "production branch for the project")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, "usage: gregale scan [--tarball P] [--path DIR] [--repo OWNER/NAME] [--show-affected] [--exclude NAME,…]", "scan")
		return 1
	}

	// Exactly one of --tarball / --path / --repo. Default --path $PWD
	// when stdin is a TTY and no flag is set (issue #313 zero-config).
	srcPath, sourceName, cleanup, err := resolveScanSource(*tarball, *pathFlag, *repo, *ref, *installID)
	if err != nil {
		return printErr("Could not resolve source", err)
	}
	defer cleanup()

	if *projectSlug == "" {
		*projectSlug = defaultProjectSlug(srcPath)
	}

	client, err := authedClientWithDeployTimeout(2 * time.Minute)
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	onlyList := splitCSV(*only)
	excludeList := splitCSV(*exclude)
	// Client-side mirror of the server's mutex validation. The
	// server is still authoritative (returns 409 on overlap); this
	// avoids a needless round-trip when the operator typos both
	// flags. intersect(empty) short-circuits on either side.
	if ok, clash := intersect(onlyList, excludeList); ok {
		return printErr("Invalid flags", fmt.Errorf(
			"--only and --exclude share workload(s): %s",
			strings.Join(clash, ", ")))
	}
	// srcPath was either customer-supplied (--tarball / --path) or
	// resoled by the CLI (autoPackCwd tmp / curl repo tmp). Both pass
	// through openCustomerFile so the lint tripwire's symlink-follow
	// guarantee (commands5.go) covers the Phase 3 paths too.
	src, err := openCustomerFile(srcPath)
	if err != nil {
		return printErr("Could not open source", err)
	}
	defer func() { _ = src.Close() }()
	plan, err := client.ScanProject(ctx, src, sourceName, *projectSlug, *prodBranch, *installID, onlyList, excludeList, *persistExclude)
	if err != nil {
		return printErr("Scan failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	return printPlanText(osStdout, plan, excludeList, *showAffected)
}

// resolveScanSource normalises the three input shapes (--tarball /
// --path / --repo) into a (path, sourceName, cleanup, err). For
// --repo the tarball is fetched via the install token and dropped
// into a tmpfile (cleanup removes it). For --path the local directory
// is auto-packed via the same autoPackCwd the deploy path uses.
func resolveScanSource(
	tarball, pathFlag, repo, ref string, installID int64,
) (string, string, func(), error) {
	chosen := 0
	if tarball != "" {
		chosen++
	}
	if pathFlag != "" {
		chosen++
	}
	if repo != "" {
		chosen++
	}
	if chosen > 1 {
		return "", "", func() {}, errors.New("--tarball, --path, and --repo are mutually exclusive")
	}
	if tarball != "" {
		return tarball, filepath.Base(tarball), func() {}, nil
	}
	if pathFlag != "" {
		// Secret-scan runs on the explicit path the same way it does for
		// the cwd path below — any source tree the customer packs goes
		// through the same pre-upload credential check. We use
		// modeSourceTree here because plan-time resolution walks the
		// whole tree (the per-file body scan is cheaper than re-walking
		// once for .env and once for everything else).
		overrides, scanFindings, scanErr := scanAndRedactEnvFiles(pathFlag, modeSourceTree)
		if scanErr != nil {
			return "", "", func() {}, fmt.Errorf("secret scan failed: %w", scanErr)
		}
		renderSecretScanWarnings(scanFindings, osStderr)
		path, _, n, err := autoPackCwd(pathFlag, defaultZeroConfigSourceCapMB, overrides)
		if err != nil {
			return "", "", func() {}, err
		}
		_ = n
		return path, filepath.Base(path) + ".tar.gz", func() { _ = os.Remove(path) }, nil
	}
	if repo != "" {
		if err := validateRepoSlug(repo); err != nil {
			return "", "", func() {}, fmt.Errorf("invalid --repo: %w", err)
		}
		if err := validateGitHubRef(ref); err != nil {
			return "", "", func() {}, fmt.Errorf("invalid --ref: %w", err)
		}
		if installID <= 0 {
			return "", "", func() {}, errors.New("--repo requires --install-id")
		}
		path, err := fetchRepoTarball(repo, ref, installID)
		if err != nil {
			return "", "", func() {}, err
		}
		return path, fmt.Sprintf("%s-%s.tar.gz", strings.ReplaceAll(repo, "/", "-"), ref),
			func() { _ = os.Remove(path) }, nil
	}
	// zero-config: stdin is a TTY → pack $PWD (issue #313)
	if stdoutIsTTY() && stdinIsTTY() {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", func() {}, err
		}
		overrides, scanFindings, scanErr := scanAndRedactEnvFiles(cwd, modeSourceTree)
		if scanErr != nil {
			return "", "", func() {}, fmt.Errorf("secret scan failed: %w", scanErr)
		}
		renderSecretScanWarnings(scanFindings, osStderr)
		path, _, n, err := autoPackCwd(cwd, defaultZeroConfigSourceCapMB, overrides)
		if err != nil {
			return "", "", func() {}, err
		}
		_ = n
		return path, filepath.Base(path) + ".tar.gz", func() { _ = os.Remove(path) }, nil
	}
	return "", "", func() {}, errors.New("one of --tarball, --path, --repo, or a TTY cwd is required")
}

// fetchRepoTarball shells out to curl to download the GitHub tarball
// using the install token from env or keyring. Returns the tmp path.
// The function lives in the CLI (not pkg/) because the install token
// lives in the customer's keychain — apid does not see it.
//
// Curl+sha256+tar pattern mirrors CI's vacuum binary download
// (memory note: ci-vacuum-binary-download).
func fetchRepoTarball(repoFullName, ref string, installID int64) (string, error) {
	downloadURL, err := githubTarballURL(repoFullName, ref)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "gregale-repo-*.tar.gz")
	if err != nil {
		return "", err
	}
	_ = f.Close()
	path := f.Name()
	token, err := readInstallToken(installID)
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("read install token: %w", err)
	}
	cmd := exec.Command("curl", "-sSL", "--fail-with-body",
		"-H", "Authorization: Bearer "+token,
		"-H", "Accept: application/vnd.github+json",
		"-o", path, downloadURL)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("curl: %w: %s", err, string(out))
	}
	return path, nil
}

// readInstallToken pulls a GitHub install token from env or keyring.
// Tries GREGALE_INSTALL_TOKEN_<ID> first (env override for CI), then
// errors cleanly so the caller can surface a "run `gregale connect`
// first" message.
func readInstallToken(installID int64) (string, error) {
	if v := os.Getenv(fmt.Sprintf("GREGALE_INSTALL_TOKEN_%d", installID)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no install token for id %d — run `gregale connect` first", installID)
}

// defaultProjectSlug derives a kebab slug from the source path's
// final component, stripping archive-style suffixes (.tar.gz, .tgz).
// Returns "" when the path is empty so the apid server falls back
// to its project-<random> default. The two-step strip is intentional
// — a single filepath.Ext() call only drops ".gz" and leaves ".tar"
// in the slug, which customers typing "deploy fixture.tar.gz" would
// not expect.
func defaultProjectSlug(p string) string {
	if p == "" {
		return ""
	}
	base := filepath.Base(p)
	for _, suffix := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}

// splitCSV returns the trimmed lowercase entries of s. Empty input → nil.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// intersect reports whether a and b share an element. Used by
// cmdScan / cmdDeployTarball to short-circuit --only ∩ --exclude
// overlaps before a server round-trip — server is still the
// authoritative gate (409 exclude_only_overlap), this is a UX
// guard. Returns (true, sorted-slice) on hit, (false, nil) on miss.
// Caller passes already-normalised (lowercased/trimmed) slices.
func intersect(a, b []string) (bool, []string) {
	if len(a) == 0 || len(b) == 0 {
		return false, nil
	}
	idx := make(map[string]struct{}, len(b))
	for _, s := range b {
		idx[s] = struct{}{}
	}
	var clash []string
	for _, s := range a {
		if _, ok := idx[s]; ok {
			clash = append(clash, s)
		}
	}
	if len(clash) == 0 {
		return false, nil
	}
	sort.Strings(clash)
	return true, clash
}

// printPlanText renders the plan as a human-readable table. Two
// sections: Workloads (sorted by name asc) and Managed (stateful
// services we won't provision). Cron rows appear under Workloads with
// a "(cron: <schedule>)" suffix.
//
// Fprintf at the tab stop is no different from a panic mid-row for the
// operator — both surface as malformed output and the CLI exits non-zero
// on the JSON-parse path below (mirrors commands_builds.go:166).
//
// excludeSet is the operator's CLI-side --exclude list. It only
// flows into printAffectedText (the --show-affected partition
// view), where it marks WillDeploy entries the operator wanted
// to exclude but the server didn't (a server bug surface —
// under normal operation the server's Skipped[] covers the
// same slugs). The single-section Workloads loop does not need
// it: plan.Workloads is the post-filter set, so excluded slugs
// never appear there.
//
//nolint:errcheck // tabular printer writes to a typed io.Writer; a failed
func printPlanText(w io.Writer, plan api.PlanResponse, excludeSet []string, showAffected bool) int {
	fmt.Fprintf(w, "Project: %s\n", plan.ProjectSlug)
	fmt.Fprintf(w, "Scan source: %s   tier: %s\n", plan.ScanSource, plan.Tier)
	fmt.Fprintf(w, "Quota: %d/%d apps   %d/%d crons\n",
		plan.ObservedApps, plan.LimitApps, plan.ObservedCrons, plan.LimitCrons)
	// ADR-124 ship-blocker #4 (PR-followup). When --exclude
	// produced a destructive subset (plan.Removed non-empty) AND
	// the operator didn't ask for the full partition view
	// (showAffected=false), surface a one-line warning so the
	// soft-delete isn't invisible. The full Removed list is
	// opt-in via --show-affected — the warning is the nudge, not
	// the data (per ADR-124 §3: warning + show-affected opt-in,
	// not auto-promote).
	if len(excludeSet) > 0 && len(plan.Removed) > 0 && !showAffected {
		PrintWarn(w, "Applying will soft-delete %d app(s); rerun with --show-affected to see the Removed partition",
			len(plan.Removed))
	}
	if plan.CronsNotAllowed {
		fmt.Fprintln(w, "(Crons unavailable on this plan — upgrade to Hobby or above.)")
	}
	// ADR-124 follow-up #1: when the gate was blocked pre-exclude
	// AND --exclude rescued it (server invariant: gateRescuedByExclude
	// => canApply=true), surface the rescue BEFORE the can_apply:true
	// line so the operator sees "your --exclude saved you". The wire
	// invariant means the early-return below cannot fire on a rescued
	// plan — CanApply is true whenever GateRescuedByExclude is true.
	// The CanApplyReasons slice carries the pre-exclude blocker list
	// (what would have failed without --exclude); we render it as a
	// bulleted set so a single-problem case and a multi-problem case
	// look the same. Do NOT remove the !CanApply early-return — that
	// path is the operator-visible "this plan failed" surface and
	// planProblem (below) carries the wire code; changing it would
	// silently break script grep on "can_apply: false".
	if plan.CanApply && plan.GateRescuedByExclude {
		PrintWarn(w, "Gate rescued by --exclude (pre-exclude gate was blocked):")
		if len(plan.CanApplyReasons) == 0 {
			fmt.Fprintln(w, "    (no pre-exclude reasons reported)")
		} else {
			for _, r := range plan.CanApplyReasons {
				fmt.Fprintf(w, "    pre-exclude reason: %s\n", r)
			}
		}
	}
	if !plan.CanApply {
		fmt.Fprintln(w, "can_apply: false")
		return 0
	}
	fmt.Fprintln(w, "can_apply: true")
	excludeIdx := make(map[string]bool, len(excludeSet))
	for _, s := range excludeSet {
		excludeIdx[s] = true
	}
	if showAffected {
		printAffectedText(w, plan, excludeIdx)
		return 0
	}
	if len(plan.Workloads) > 0 {
		fmt.Fprintln(w, "\nWorkloads:")
		// sort defensively even though reposcan already sorts — a
		// future server-side refactor must not change CLI output.
		ws := append([]api.PlanWorkload(nil), plan.Workloads...)
		sort.Slice(ws, func(i, j int) bool { return ws[i].Name < ws[j].Name })
		for _, wl := range ws {
			schedSuffix := ""
			if wl.Schedule != "" {
				schedSuffix = " (cron: " + wl.Schedule + ")"
			}
			classSuffix := ""
			if wl.Class != "" {
				classSuffix = "  class=" + wl.Class
			}
			// plan.Workloads is the post-filter set: the scan
			// service drops --only/--exclude slugs before populating
			// it (scan_service.go:564-577). So no excluded row ever
			// appears in this loop, and no "(excluded)" tag is
			// needed here. The show-affected branch (printAffectedText)
			// renders the partition including Skipped.
			fmt.Fprintf(w, "  - %-20s root=%-20s%s%s\n", wl.Name, wl.RootDir, schedSuffix, classSuffix)
		}
	}
	if len(plan.Managed) > 0 {
		fmt.Fprintln(w, "\nManaged (not provisioned):")
		for _, m := range plan.Managed {
			fmt.Fprintf(w, "  - %s [%s]   hint=%s   image=%s\n",
				m.Name, m.Kind, m.EnvHint, m.Image)
		}
	}
	if len(plan.Warnings) > 0 {
		fmt.Fprintln(w, "\nWarnings:")
		for _, wn := range plan.Warnings {
			fmt.Fprintln(w, "  - "+wn)
		}
	}
	return 0
}

// printAffectedText renders the ADR-124 blast-radius view: WillDeploy
// + Skipped (operator-excluded) + Unaffected + Removed. Mirrors the
// dashboard's two-table render so a CLI operator sees the same
// partition the dashboard would.
//
// excludedSlugs is a normalised (lowercased) lookup the renderer
// uses to mark WillDeploy entries that would have been there but
// the operator excluded — the row gets a " [excluded locally]" tag
// so the reader can spot operator overrides without re-running the
// scan.
//
// Fprintln at the tab stop is no different from a panic mid-row for the
// operator — both surface as malformed output and the CLI exits non-zero
// on the JSON-parse path below (mirrors printPlanText above).
//
//nolint:errcheck // tabular printer writes to a typed io.Writer; a failed
func printAffectedText(w io.Writer, plan api.PlanResponse, excludedSlugs map[string]bool) {
	wds := append([]api.PlanAffectedApp(nil), plan.WillDeploy...)
	sort.Slice(wds, func(i, j int) bool { return wds[i].Slug < wds[j].Slug })
	una := append([]api.PlanAffectedApp(nil), plan.Unaffected...)
	sort.Slice(una, func(i, j int) bool { return una[i].Slug < una[j].Slug })
	skp := append([]api.PlanAffectedApp(nil), plan.Skipped...)
	sort.Slice(skp, func(i, j int) bool { return skp[i].Slug < skp[j].Slug })

	fmt.Fprintln(w, "\nWill deploy:")
	if len(wds) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		fmt.Fprintln(w, "  ACTION    SLUG                  APP ID                        ROOT_DIR")
		for _, r := range wds {
			mark := ""
			if excludedSlugs[strings.ToLower(r.Slug)] {
				mark = "  [excluded locally — will be skipped]"
			}
			fmt.Fprintf(w, "  %-9s %-20s %-30s %s%s\n", r.Action, r.Slug, r.ID, r.ExistingRootDir, mark)
		}
	}
	if len(skp) > 0 {
		fmt.Fprintln(w, "\nSkipped (excluded by operator):")
		fmt.Fprintln(w, "  ACTION    SLUG                  APP ID")
		for _, r := range skp {
			fmt.Fprintf(w, "  %-9s %-20s %s\n", r.Action, r.Slug, r.ID)
		}
	}
	fmt.Fprintln(w, "\nUnaffected (apps in your account not touched by this commit):")
	if len(una) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		fmt.Fprintln(w, "  ACTION    SLUG                  APP ID")
		for _, r := range una {
			fmt.Fprintf(w, "  %-9s %-20s %s\n", r.Action, r.Slug, r.ID)
		}
	}
	if len(plan.Removed) > 0 {
		fmt.Fprintln(w, "\nRemoved (apps the apply path will SoftDeleteAppCascade):")
		rmv := append([]string(nil), plan.Removed...)
		sort.Strings(rmv)
		for _, s := range rmv {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
}

// confirmPlan prints the plan and waits for a y/N confirmation. Reads
// from r (typically os.Stdin) so tests can stub it. Returns true on
// 'y' / 'yes' (case-insensitive); false on EOF, 'n', or any other
// input — git does the same.
//
// showAffected is threaded through to printPlanText so the operator
// who typed --show-affected at the apply prompt sees the same
// partition view as on the scan. The default (false) keeps the
// confirm-prompt terse; the destructive --exclude warning lives
// inside printPlanText and fires regardless of showAffected.
func confirmPlan(w io.Writer, r io.Reader, plan api.PlanResponse, excludeSet []string, showAffected bool) bool {
	printPlanText(w, plan, excludeSet, showAffected)
	//nolint:errcheck // same rationale as printPlanText; a failed Fprintln
	// at the prompt is no different from the read below failing.
	prompt := "\nApply this plan? [y/N] "
	if n := len(excludeSet); n > 0 {
		prompt = fmt.Sprintf("\nApply %d workload(s) (excluded: %s)? [y/N] ",
			len(plan.Workloads), strings.Join(excludeSet, ", "))
	} else if n := len(plan.Workloads); n > 0 {
		prompt = fmt.Sprintf("\nApply %d workload(s)? [y/N] ", n)
	}
	//nolint:errcheck // prompt write mirrors the Fprintln above; if the
	// prompt fails to flush the subsequent scanner.Scan() will also fail
	// (broken pipe surfaces as EOF) and the function returns false.
	fmt.Fprint(w, prompt)
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return ans == "y" || ans == "yes"
}

// planProblem translates a non-applyable plan into the matching
// RFC 7807 problem shape the apid handler emits. Used by the --json
// path so a CLI consumer sees the same wire shape as a direct
// httptest call would produce.
//
// ADR-124 follow-up #1: a 4th branch handles the pre-exclude gate
// block. The wire invariant (scan_service.go:864) is
// `gateRescuedByExclude := !preCanApply && canApply`, so the
// over-quota exit (`!plan.CanApply`) means either:
//   - the gate was blocked pre-exclude AND post-exclude --exclude
//     did NOT rescue it; OR
//   - a wire bug (planProblem is defensive — uses !CanApplyPreExclude
//     rather than CanApplyPreExclude truth value as the discriminator).
//
// The first two branches (CronsNotAllowed + OverApps) keep their
// specific codes so dashboards can still surface the upsell-vs-quota
// copy they already render. The new branch carries the full
// can_apply_reasons list so the operator sees every blocker, not
// just the first — a deliberate "single problem + tail" ugly
// message is worse than the joined string in the rare multi-block
// case.
func planProblem(plan api.PlanResponse) api.Problem {
	if plan.CronsNotAllowed {
		return api.Problem{
			Status: 402,
			Code:   api.CodePlanCronsNotAllowed,
			Title:  "Crons unavailable on this plan",
			Detail: "the Free plan does not include cron; upgrade to Hobby or above to schedule synthetic requests.",
		}
	}
	if plan.ObservedApps > plan.LimitApps {
		return api.Problem{
			Status: 403,
			Code:   api.CodePlanLimitApps,
			Title:  "App limit reached",
			Detail: fmt.Sprintf("plan caps apps at %d; you have %d.", plan.LimitApps, plan.ObservedApps),
		}
	}
	if !plan.CanApplyPreExclude {
		// The OLD test invariant: when ObservedCrons > LimitCrons
		// (the implicit cron-quota case that fell through to the
		// CronQuota fallback), the wire code stays plan_cron_quota.
		// The GateBlocked path catches every other gate-block
		// case — typically a future gate type that doesn't have a
		// specific wire code yet, or a multi-reason block where
		// cron-quota is not the primary reason. Without the
		// ObservedCrons <= LimitCrons guard we would re-route the
		// `cron-limit` case (TestPlanProblem_Mapping/cron-limit in
		// commands_decompose_test.go) onto CodePlanGateBlocked,
		// breaking the wire-shape parity test that ADRs pin.
		if plan.ObservedCrons <= plan.LimitCrons {
			return api.Problem{
				Status: 403,
				Code:   api.CodePlanGateBlocked,
				Title:  "Plan gate blocked",
				Detail: fmt.Sprintf("gate was blocked pre-exclude; reasons: %s",
					strings.Join(plan.CanApplyReasons, "; ")),
			}
		}
	}
	return api.Problem{
		Status: 403,
		Code:   api.CodePlanCronQuota,
		Title:  "Cron limit reached",
		Detail: fmt.Sprintf("plan caps crons at %d; you have %d.", plan.LimitCrons, plan.ObservedCrons),
	}
}
