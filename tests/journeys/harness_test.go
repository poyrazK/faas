// Package journeys is the black-box customer journey suite: it drives a
// deployed Gregale only through the public API, the public app URLs and
// the released `gregale` CLI, the way a customer does. It never touches
// Postgres, KVM or a daemon, so it runs from a laptop, CI or an agent
// session in minutes. See tests/journeys/README.md and
// scripts/e2e-journeys.sh.
package journeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	tierSmoke = "smoke"
	tierFull  = "full"

	// slugPrefix marks every app the suite creates. The sweeper deletes
	// only slugs that parse as e2ej-<run>-<name>, so it can run against
	// an account that also holds hand-made apps.
	slugPrefix  = "e2ej-"
	maxSlugLen  = 32
	deployLimit = 20 * time.Minute
)

// target is the deployed Gregale the journeys run against.
type target struct {
	api        string
	appsDomain string
	token      string
	useLogin   bool // no token: let the CLI use its own `gregale login`
	cli        string
	runID      string
	tier       string
	keep       bool
	home       string // isolated HOME and working directory for the CLI

	preflightOnce sync.Once
	preflightErr  error
	deploySlots   chan struct{} // bounds concurrent deploys (GREGALE_E2E_MAX_DEPLOYS)
	maxApps       int           // GREGALE_E2E_MAX_APPS; 0 sizes appSlots from account headroom
	appSlots      chan struct{} // bounds apps held at once; made by the preflight

	// The shared probe is one v1 probe app that read-only journeys share.
	sharedOnce sync.Once
	sharedSlug string
	sharedBase string
	sharedErr  error
}

// tgt is nil when GREGALE_E2E_API is unset; every journey then skips, so
// `go test ./...` stays green on machines with no target.
var tgt *target

func TestMain(m *testing.M) {
	t, err := loadTarget(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "journeys:", err)
		os.Exit(2)
	}
	tgt = t
	if tgt != nil {
		home, err := os.MkdirTemp("", "gregale-journeys-home-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "journeys:", err)
			os.Exit(2)
		}
		tgt.home = home
		fmt.Fprintf(os.Stderr, "journeys: target=%s apps=%s run=%s tier=%s cli=%s\n",
			tgt.api, tgt.appsDomain, tgt.runID, tgt.tier, tgt.cli)
		if hours := os.Getenv("GREGALE_E2E_SWEEP_HOURS"); hours != "" && tgt.hasAuth() {
			if err := tgt.sweep(hours); err != nil {
				fmt.Fprintln(os.Stderr, "journeys: sweep:", err)
			}
		}
	}
	code := m.Run()
	if tgt != nil {
		tgt.deleteShared()
		_ = os.RemoveAll(tgt.home)
	}
	os.Exit(code)
}

func loadTarget(getenv func(string) string) (*target, error) {
	api := strings.TrimRight(getenv("GREGALE_E2E_API"), "/")
	if api == "" {
		return nil, nil
	}
	u, err := url.Parse(api)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("GREGALE_E2E_API %q is not an http(s) origin", api)
	}
	t := &target{
		api:        api,
		appsDomain: getenv("GREGALE_E2E_APPS_DOMAIN"),
		token:      getenv("GREGALE_E2E_TOKEN"),
		useLogin:   getenv("GREGALE_E2E_AUTH") == "cli-login",
		cli:        getenv("GREGALE_E2E_CLI"),
		runID:      getenv("GREGALE_E2E_RUN_ID"),
		tier:       getenv("GREGALE_E2E_TIER"),
		keep:       getenv("GREGALE_E2E_KEEP") == "1",
	}
	if t.appsDomain == "" {
		t.appsDomain = strings.TrimPrefix(u.Hostname(), "api.")
	}
	if t.useLogin && t.token != "" {
		return nil, fmt.Errorf("GREGALE_E2E_AUTH=cli-login and GREGALE_E2E_TOKEN are mutually exclusive")
	}
	if t.cli == "" {
		t.cli = "gregale"
	}
	if t.tier == "" {
		t.tier = tierSmoke
	}
	if t.tier != tierSmoke && t.tier != tierFull {
		return nil, fmt.Errorf("GREGALE_E2E_TIER %q: want %s or %s", t.tier, tierSmoke, tierFull)
	}
	if t.runID == "" {
		t.runID = newRunID(time.Now())
	}
	// Four at once: unchanged sources hit the build cache and skip the
	// builder VM, so the 20 GiB builder drive no longer bounds them.
	maxDeploys, err := positiveEnv(getenv, "GREGALE_E2E_MAX_DEPLOYS", 4)
	if err != nil {
		return nil, err
	}
	t.deploySlots = make(chan struct{}, maxDeploys)
	if t.maxApps, err = positiveEnv(getenv, "GREGALE_E2E_MAX_APPS", 0); err != nil {
		return nil, err
	}
	if _, ok := runTime(t.runID); !ok {
		return nil, fmt.Errorf("GREGALE_E2E_RUN_ID %q: want 5 base36 minute digits plus one random character", t.runID)
	}
	return t, nil
}

func positiveEnv(getenv func(string) string, key string, def int) (int, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s %q: want a positive integer", key, v)
	}
	return n, nil
}

// newRunID encodes the start minute so the sweeper can age apps without a
// created_at field: five base36 digits of Unix minutes plus one random
// character to separate runs started in the same minute.
func newRunID(now time.Time) string {
	minutes := strconv.FormatInt(now.Unix()/60, 36)
	for len(minutes) < 5 {
		minutes = "0" + minutes
	}
	n, err := rand.Int(rand.Reader, big.NewInt(36))
	if err != nil {
		n = big.NewInt(0)
	}
	return minutes + strconv.FormatInt(n.Int64(), 36)
}

func runTime(runID string) (time.Time, bool) {
	if len(runID) != 6 || strings.ToLower(runID) != runID {
		return time.Time{}, false
	}
	minutes, err := strconv.ParseInt(runID[:5], 36, 64)
	if err != nil {
		return time.Time{}, false
	}
	if _, err := strconv.ParseInt(runID[5:], 36, 64); err != nil {
		return time.Time{}, false
	}
	return time.Unix(minutes*60, 0).UTC(), true
}

var journeySlugRe = regexp.MustCompile(`^e2ej-([0-9a-z]{6})-[a-z0-9]+(-[a-z0-9]+)*$`)

// slugRun returns the run id of a slug the suite created.
func slugRun(slug string) (string, bool) {
	m := journeySlugRe.FindStringSubmatch(slug)
	if m == nil {
		return "", false
	}
	if _, ok := runTime(m[1]); !ok {
		return "", false
	}
	return m[1], true
}

func (tg *target) slug(name string) string {
	s := slugPrefix + tg.runID + "-" + name
	if len(s) > maxSlugLen || !journeySlugRe.MatchString(s) {
		panic(fmt.Sprintf("journey app name %q makes invalid slug %q", name, s))
	}
	return s
}

// needs skips a journey the configured target cannot run.
func needs(t *testing.T, tier string, auth bool) *target {
	t.Helper()
	if tgt == nil {
		t.Skip("no target: set GREGALE_E2E_API (see tests/journeys/README.md)")
	}
	if tier == tierFull && tgt.tier != tierFull {
		t.Skip("full tier only: set GREGALE_E2E_TIER=full")
	}
	if auth && !tgt.hasAuth() {
		t.Skip("needs an account: set GREGALE_E2E_TOKEN or GREGALE_E2E_AUTH=cli-login")
	}
	return tgt
}

// --- CLI ---------------------------------------------------------------

type cliResult struct {
	args    []string
	stdout  []byte
	stderr  []byte
	exit    int
	elapsed time.Duration
}

// problem is the RFC 7807 body the CLI prints to stderr in JSON mode.
func (r cliResult) problem() string {
	var p struct {
		Title, Code, Detail string
	}
	if json.Unmarshal(bytes.TrimSpace(r.stderr), &p) == nil && (p.Code != "" || p.Detail != "") {
		return fmt.Sprintf("%s (%s): %s", p.Title, p.Code, p.Detail)
	}
	return tail(string(r.stderr), 2000)
}

func (tg *target) hasAuth() bool { return tg.token != "" || tg.useLogin }

// cliEnv isolates the CLI from the operator's machine: with an explicit
// token it gets a private HOME, so no local login, config or linked project
// leaks in. In cli-login mode with no explicit token it keeps the real HOME
// so the CLI finds its own stored login (the macOS keychain); the working
// directory is still a private one.
func (tg *target) cliEnv(token string) []string {
	ownLogin := tg.useLogin && token == ""
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "FAAS_") || strings.HasPrefix(k, "GREGALE_") {
			continue
		}
		if !ownLogin && (k == "HOME" || k == "XDG_CONFIG_HOME") {
			continue
		}
		env = append(env, kv)
	}
	if !ownLogin {
		env = append(env,
			"HOME="+tg.home,
			"XDG_CONFIG_HOME="+filepath.Join(tg.home, ".config"),
			"FAAS_TOKEN="+token,
		)
	}
	return append(env,
		"FAAS_API="+tg.api,
		"FAAS_APPS_DOMAIN="+tg.appsDomain,
		"FAAS_JSON=1",
	)
}

// run executes the CLI with the target's token. It never fails the test;
// see ok for the asserting form.
func (tg *target) run(t *testing.T, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	return tg.exec(t, tg.token, tg.home, timeout, args...)
}

// runAs runs the CLI with another credential, e.g. a freshly minted key.
func (tg *target) runAs(t *testing.T, token string, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	return tg.exec(t, token, tg.home, timeout, args...)
}

// exec runs the CLI in dir. Deploys get their own directory because the
// CLI scaffolds templates and writes link state relative to it.
func (tg *target) exec(t *testing.T, token, dir string, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tg.cli, append([]string{"--non-interactive"}, args...)...)
	cmd.Env = tg.cliEnv(token)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	res := cliResult{args: args, stdout: stdout.Bytes(), stderr: stderr.Bytes(), elapsed: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.exit = exitErr.ExitCode()
	default:
		res.exit = -1
		res.stderr = append(res.stderr, []byte("\n"+err.Error())...)
	}
	if ctx.Err() != nil {
		res.stderr = append(res.stderr, []byte(fmt.Sprintf("\nkilled after %s timeout", timeout))...)
	}
	t.Logf("$ gregale %s  -> exit %d in %s", strings.Join(args, " "), res.exit, res.elapsed.Round(time.Millisecond))
	return res
}

// ok runs the CLI and fails the test on a non-zero exit.
func (tg *target) ok(t *testing.T, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	res := tg.run(t, timeout, args...)
	if res.exit != 0 {
		t.Fatalf("gregale %s failed (exit %d): %s\nstdout: %s", strings.Join(args, " "), res.exit,
			tg.redact(res.problem()), tg.redact(tail(string(res.stdout), 2000)))
	}
	return res
}

// objects decodes the CLI's JSON output: one document (scalars print
// indented JSON) or NDJSON (slices print one object per line).
func objects(t *testing.T, out []byte) []map[string]any {
	t.Helper()
	docs, err := decodeObjects(out)
	if err != nil {
		t.Fatalf("decode CLI JSON: %v\noutput: %s", err, tail(string(out), 2000))
	}
	return docs
}

func decodeObjects(out []byte) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	var docs []map[string]any
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
}

func object(t *testing.T, out []byte) map[string]any {
	t.Helper()
	docs := objects(t, out)
	if len(docs) != 1 {
		t.Fatalf("want one JSON object, got %d\noutput: %s", len(docs), tail(string(out), 2000))
	}
	return docs[0]
}

// field reads a dotted path ("hosting_receipt.smoke.status", "items.0.id").
func field(v any, path string) any {
	for _, part := range strings.Split(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			v = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			v = node[i]
		default:
			return nil
		}
	}
	return v
}

func str(v any, path string) string {
	switch s := field(v, path).(type) {
	case string:
		return s
	case json.Number:
		return s.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(s)
	}
}

func num(v any, path string) float64 {
	switch n := field(v, path).(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	default:
		return 0
	}
}

func compact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return tail(string(b), 1500)
}

// --- apps --------------------------------------------------------------

// app registers an app slug for deletion when the test ends. With
// GREGALE_E2E_KEEP=1 a failed journey keeps its app for debugging; the
// sweeper removes it later.
//
// Each app holds one of the account's free deployed-app slots until it is
// deleted; a journey waits here when the run already holds them all, so
// parallel journeys fit any account.
func (tg *target) app(t *testing.T, name string) string {
	t.Helper()
	tg.acquireAppSlot(t)
	slug := tg.slug(name)
	t.Cleanup(func() {
		defer func() { <-tg.appSlots }()
		if tg.keep && t.Failed() {
			t.Logf("keeping %s for debugging (GREGALE_E2E_KEEP=1)", slug)
			return
		}
		res := tg.run(t, 2*time.Minute, "apps", "rm", slug, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("cleanup: delete %s: %s", slug, res.problem())
		}
	})
	return slug
}

func (tg *target) acquireAppSlot(t *testing.T) {
	t.Helper()
	tg.preflightOnce.Do(func() {
		if os.Getenv("GREGALE_E2E_SKIP_PREFLIGHT") == "1" {
			tg.appSlots = make(chan struct{}, max(tg.maxApps, 8))
			return
		}
		tg.preflightErr = tg.preflight()
	})
	if tg.preflightErr != nil {
		t.Fatalf("preflight: %v", tg.preflightErr)
	}
	start := time.Now()
	tg.appSlots <- struct{}{}
	if waited := time.Since(start); waited > time.Second {
		t.Logf("waited %s for a free app slot", waited.Round(time.Second))
	}
}

// sharedProbe returns the run's shared v1 probe app, deploying it on first
// use. Journeys that use it may request it and read its logs, metrics and
// crons, but must not change its configuration, version or lifecycle.
func (tg *target) sharedProbe(t *testing.T) (slug, base string) {
	t.Helper()
	tg.sharedOnce.Do(func() {
		// Set before deploying: a deploy failure exits this goroutine
		// (t.Fatal) and later callers must still see an error.
		tg.sharedErr = fmt.Errorf("the shared probe app failed to deploy in %s", t.Name())
		tg.acquireAppSlot(t)
		tg.sharedSlug = tg.slug("shared")
		receipt := tg.deploy(t, tg.sharedSlug, "--path", probeSource(t, "v1"))
		tg.sharedBase = strings.TrimRight(str(receipt, "app_url"), "/")
		if tg.sharedBase == "" {
			tg.sharedBase = tg.appURL(tg.sharedSlug)
		}
		tg.sharedErr = nil
	})
	if tg.sharedErr != nil {
		t.Fatal(tg.sharedErr)
	}
	return tg.sharedSlug, tg.sharedBase
}

// deleteShared removes the shared probe once every journey has finished.
func (tg *target) deleteShared() {
	if tg.sharedSlug == "" {
		return
	}
	cmd := exec.Command(tg.cli, "--non-interactive", "apps", "rm", tg.sharedSlug, "--yes")
	cmd.Env = tg.cliEnv(tg.token)
	cmd.Dir = tg.home
	if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "not_found") {
		fmt.Fprintf(os.Stderr, "journeys: delete shared probe %s: %v: %s\n", tg.sharedSlug, err, tail(string(out), 300))
	}
}

// deploy runs `gregale deploy --wait` and checks the receipt is live,
// rollout-complete and smoke-verified. An infrastructure-class failure is
// retried once, like deploy/scripts/production-release-acceptance.sh.
func (tg *target) deploy(t *testing.T, slug string, args ...string) map[string]any {
	t.Helper()
	full := append([]string{"deploy", "--name", slug, "--wait", "--timeout", deployLimit.String(),
		"--yes", "--no-require-authn", "--reason", "e2e-journeys run " + tg.runID}, args...)
	dir := t.TempDir()
	tg.deploySlots <- struct{}{}
	defer func() { <-tg.deploySlots }()
	res := tg.exec(t, tg.token, dir, deployLimit+5*time.Minute, full...)
	if reason := retryableAbort(res); reason != "" {
		// Reported, not hidden: the report lists every warning line.
		t.Logf("warning: deploy %s aborted for capacity (%s); retrying once in 60s", slug, reason)
		time.Sleep(60 * time.Second)
		res = tg.exec(t, tg.token, dir, deployLimit+5*time.Minute, full...)
	}
	if res.exit != 0 {
		t.Fatalf("deploy %s failed (exit %d): %s\nreceipt: %s\nlog tail:\n%s", slug, res.exit, res.problem(),
			tail(string(res.stdout), 1500), tg.deployLogTail(t, slug, res.stdout))
	}
	receipt := object(t, res.stdout)
	// A redeploy's --wait returns once the candidate is live and verified,
	// while traffic is still shifting; follow the rollout like
	// production-release-acceptance.sh does. The default safe ladder
	// (adaptive: 1/10/50%, up to 2 min each) may hold each stage another
	// 5 min on a low-traffic app (ADR-1446), so allow ~21 min plus margin.
	if str(receipt, "status") == "live" && str(receipt, "rollout_state") == "rolling_out" {
		waited := tg.ok(t, 27*time.Minute, "deployment", "wait", str(receipt, "id"),
			"--rollout", "--timeout", "25m")
		final := object(t, waited.stdout)
		receipt["rollout_state"] = field(final, "rollout_state")
		receipt["status"] = field(final, "status")
	}
	if str(receipt, "status") != "live" || str(receipt, "rollout_state") != "complete" ||
		str(receipt, "hosting_receipt.smoke.status") != "verified" {
		t.Fatalf("deploy %s receipt is not live, rollout-complete and smoke-verified: %s", slug,
			compact(map[string]any{"id": field(receipt, "id"), "status": field(receipt, "status"),
				"rollout_state": field(receipt, "rollout_state"), "smoke": field(receipt, "hosting_receipt.smoke")}))
	}
	t.Logf("deployed %s as %s in %s", slug, str(receipt, "id"), res.elapsed.Round(time.Second))
	return receipt
}

// retryableAbort names a failed deploy's platform-capacity cause: an
// infrastructure-class failure, a node at its RAM admission ceiling, or a
// builder short of drive space. A broken app or release fails at once.
func retryableAbort(res cliResult) string {
	if res.exit == 0 {
		return ""
	}
	docs, err := decodeObjects(res.stdout)
	if err != nil || len(docs) == 0 {
		return ""
	}
	if str(docs[0], "failure_class") == "infra" {
		return "failure_class=infra"
	}
	reason := str(docs[0], "rollout_aborted_reason")
	for _, capacity := range []string{"capacity_unavailable", "insufficient host free space"} {
		if strings.Contains(reason, capacity) {
			return reason
		}
	}
	return ""
}

func (tg *target) deployLogTail(t *testing.T, slug string, receipt []byte) string {
	t.Helper()
	docs, err := decodeObjects(receipt)
	if err != nil || len(docs) == 0 || str(docs[0], "id") == "" {
		return "(no deployment id in the receipt)"
	}
	// Build and deploy log; `logs --deployment` reads runtime logs only.
	res := tg.run(t, time.Minute, "deploys", "logs", str(docs[0], "id"), "--limit", "60")
	return tail(string(res.stdout)+string(res.stderr), 4000)
}

func (tg *target) appURL(slug string) string {
	return "https://" + slug + "." + tg.appsDomain
}

// probeSource copies the probe app into a fresh directory with version
// baked in, so `GET /` proves which revision is serving.
func probeSource(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("testdata", "probe-node")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read probe source: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read probe source: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatalf("write probe source: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatalf("write probe version: %v", err)
	}
	return dir
}

// --- HTTP --------------------------------------------------------------

type httpResult struct {
	status  int
	header  http.Header
	body    []byte
	elapsed time.Duration
	err     error
}

func (r httpResult) String() string {
	if r.err != nil {
		return "error: " + r.err.Error()
	}
	body := tail(string(r.body), 500)
	if strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
		body = "HTML page"
		if m := htmlTitleRe.FindSubmatch(r.body); m != nil {
			body += " titled " + strconv.Quote(strings.TrimSpace(string(m[1])))
		}
	}
	return fmt.Sprintf("HTTP %d in %s: %s", r.status, r.elapsed.Round(time.Millisecond), body)
}

var htmlTitleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

var httpClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func fetch(method, rawURL string, header map[string]string) httpResult {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return httpResult{err: err}
	}
	req.Header.Set("User-Agent", "gregale-e2e-journeys")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return httpResult{err: err, elapsed: time.Since(start)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return httpResult{status: resp.StatusCode, header: resp.Header, body: body, elapsed: time.Since(start), err: err}
}

func get(rawURL string) httpResult { return fetch(http.MethodGet, rawURL, nil) }

// eventually polls check until it reports done or the timeout passes, and
// fails with the last observation.
func eventually(t *testing.T, what string, timeout, every time.Duration, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	start := time.Now()
	last := ""
	for {
		done, observed := check()
		if done {
			t.Logf("%s after %s", what, time.Since(start).Round(time.Millisecond))
			return
		}
		last = observed
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within %s; last: %s", what, timeout, last)
		}
		time.Sleep(every)
	}
}

func jsonBody(t *testing.T, r httpResult) map[string]any {
	t.Helper()
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(r.body))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode response body: %v (%s)", err, r)
	}
	return doc
}

// --- stories -----------------------------------------------------------

// story runs ordered steps as subtests. After a blocking step fails, the
// rest are reported as skipped with the step that blocked them, so the
// report shows where a journey broke instead of a cascade of failures.
type story struct {
	t       *testing.T
	blocked string
}

func newStory(t *testing.T) *story { return &story{t: t} }

// step runs a step later steps depend on.
func (s *story) step(name string, fn func(t *testing.T)) bool {
	s.t.Helper()
	if s.blocked != "" {
		blocked := s.blocked
		s.t.Run(name, func(t *testing.T) { t.Skipf("blocked: step %q failed", blocked) })
		return false
	}
	if !s.t.Run(name, fn) {
		s.blocked = name
		return false
	}
	return true
}

// check runs a step whose failure does not block the rest.
func (s *story) check(name string, fn func(t *testing.T)) {
	s.t.Helper()
	if s.blocked != "" {
		blocked := s.blocked
		s.t.Run(name, func(t *testing.T) { t.Skipf("blocked: step %q failed", blocked) })
		return
	}
	s.t.Run(name, fn)
}

// --- preflight ---------------------------------------------------------

// preflight runs before the first app is created. It sizes appSlots to the
// account's free deployed-app slots (capped by GREGALE_E2E_MAX_APPS), so a
// run never hits the plan's app limit mid-way; with no free slot at all it
// fails the app journeys up front. Journeys that create no apps still run.
func (tg *target) preflight() error {
	who, err := tg.cliJSON("whoami")
	if err != nil {
		return fmt.Errorf("whoami: %w (is GREGALE_E2E_TOKEN valid for %s?)", err, tg.api)
	}
	if len(who) != 1 {
		return fmt.Errorf("whoami printed %d JSON documents", len(who))
	}
	apps, err := tg.cliJSON("apps")
	if err != nil {
		return fmt.Errorf("list apps: %w", err)
	}
	limit := int(num(who[0], "limits.deployed_apps"))
	slots := appSlotCount(limit, len(apps), tg.maxApps)
	fmt.Fprintf(os.Stderr, "journeys: account %s plan=%s apps=%d/%d; running up to %d test apps at once\n",
		str(who[0], "email"), str(who[0], "plan"), len(apps), limit, slots)
	if slots < 1 {
		return fmt.Errorf("account %s holds %d of %d deployed apps, leaving no slot for a test app; "+
			"use a dedicated test account or delete apps first (GREGALE_E2E_SKIP_PREFLIGHT=1 overrides)",
			str(who[0], "email"), len(apps), limit)
	}
	tg.appSlots = make(chan struct{}, slots)
	return nil
}

// appSlotCount is how many test apps a run may hold at once: the account's
// free slots, capped by maxApps when set. An unknown limit (0) means 8.
func appSlotCount(limit, used, maxApps int) int {
	free := 8
	if limit > 0 {
		free = limit - used
	}
	if maxApps > 0 && maxApps < free {
		free = maxApps
	}
	return max(free, 0)
}

// cliJSON runs a read-only CLI command outside any test.
func (tg *target) cliJSON(args ...string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tg.cli, append([]string{"--non-interactive"}, args...)...)
	cmd.Env = tg.cliEnv(tg.token)
	cmd.Dir = tg.home
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, tg.redact(tail(stderr.String(), 500)))
	}
	return decodeObjects(out)
}

// --- sweeper -----------------------------------------------------------

// sweep deletes apps this suite created in earlier runs older than the
// given number of hours, so a crashed run cannot exhaust the account's
// deployed-app limit.
func (tg *target) sweep(hours string) error {
	h, err := strconv.ParseFloat(hours, 64)
	if err != nil || h <= 0 {
		return fmt.Errorf("GREGALE_E2E_SWEEP_HOURS %q: want a positive number", hours)
	}
	docs, err := tg.cliJSON("apps")
	if err != nil {
		return fmt.Errorf("list apps: %w", err)
	}
	stale := staleSlugs(docs, time.Now().Add(-time.Duration(h*float64(time.Hour))), tg.runID)
	for _, slug := range stale {
		rm := exec.Command(tg.cli, "--non-interactive", "apps", "rm", slug, "--yes")
		rm.Env = tg.cliEnv(tg.token)
		rm.Dir = tg.home
		if out, err := rm.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "journeys: sweep %s: %v: %s\n", slug, err, tail(string(out), 300))
			continue
		}
		fmt.Fprintf(os.Stderr, "journeys: swept stale app %s\n", slug)
	}
	return nil
}

func staleSlugs(apps []map[string]any, before time.Time, currentRun string) []string {
	var stale []string
	for _, app := range apps {
		slug := str(app, "slug")
		run, ok := slugRun(slug)
		if !ok || run == currentRun {
			continue
		}
		if started, _ := runTime(run); started.Before(before) {
			stale = append(stale, slug)
		}
	}
	sort.Strings(stale)
	return stale
}

// --- helpers -----------------------------------------------------------

// apiKeyRe matches Gregale API key plaintexts, which `keys add` prints.
var apiKeyRe = regexp.MustCompile(`fp_(live|test)_[A-Za-z0-9_]{16,}`)

func (tg *target) redact(s string) string {
	if tg != nil && len(tg.token) >= 8 {
		s = strings.ReplaceAll(s, tg.token, "<token>")
	}
	return apiKeyRe.ReplaceAllString(s, "<api-key>")
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	const hexDigits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(16))
		if err != nil {
			t.Fatalf("random: %v", err)
		}
		b[i] = hexDigits[v.Int64()]
	}
	return string(b)
}
