package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Candidate workers use a separate app: replacing a worker generation in place
// lets the scheduler retire old workers before the Task gate can check readiness.
type mcpNativeReleasePlan struct {
	WebApp             string   `json:"web_app"`
	WebPath            string   `json:"web_path"`
	WorkerApp          string   `json:"worker_app"`
	WorkerPath         string   `json:"worker_path"`
	PreviousWorkerApps []string `json:"previous_worker_apps"`
	ObserverApp        string   `json:"observer_app"`
	ObserverMetricApp  string   `json:"observer_metric_app"`
	TokenEnv           string   `json:"token_env,omitempty"`
	ReleasePolicy      string   `json:"release_policy,omitempty"`
	TimeoutSeconds     int      `json:"timeout_seconds"`
}
type mcpNativeReleaseState struct {
	ReleaseID           string            `json:"release_id,omitempty"`
	Version             int               `json:"version"`
	Fingerprint         string            `json:"fingerprint"`
	Stage               string            `json:"stage"`
	WebDeployment       string            `json:"web_deployment,omitempty"`
	WorkerDeployment    string            `json:"worker_deployment,omitempty"`
	ServingDeployment   string            `json:"serving_deployment"`
	ServingCaptured     bool              `json:"serving_captured"`
	PreviousDeployments map[string]string `json:"previous_deployments,omitempty"`
	WorkerIDs           []string          `json:"worker_ids,omitempty"`
	Promoted            bool              `json:"promoted"`
	Parked              map[string]bool   `json:"parked,omitempty"`
	PendingSubmission   string            `json:"pending_submission,omitempty"`
}

func readMCPNativePlan(path string) (mcpNativeReleasePlan, error) {
	var p mcpNativeReleasePlan
	f, err := openCustomerFile(path)
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, err
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return p, errors.New("release plan must contain one JSON object")
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 300
	}
	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 600 || p.WebPath == "" || p.WorkerPath == "" || p.ObserverMetricApp == "" || len(p.PreviousWorkerApps) > 32 {
		return p, errors.New("release requires source paths, observer metric app and a 1-600 second deadline")
	}
	seen := map[string]bool{}
	for _, slug := range append([]string{p.WebApp, p.WorkerApp, p.ObserverApp}, p.PreviousWorkerApps...) {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(slug) || seen[slug] {
			return p, errors.New("web, observer, candidate and previous worker apps must be distinct valid slugs")
		}
		seen[slug] = true
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return p, err
	}
	for _, value := range []*string{&p.WebPath, &p.WorkerPath, &p.ReleasePolicy} {
		if *value != "" && !filepath.IsAbs(*value) {
			*value = filepath.Join(base, *value)
		}
	}
	// Source paths are relative to the plan, never to an adapter's working directory.
	return p, nil
}

func mcpNativeFingerprint(p mcpNativeReleasePlan) (string, error) {
	h := sha256.New()
	b, _ := json.Marshal(p)
	_, _ = h.Write(b)
	_, _ = fmt.Fprintf(h, "namespace:%s\napi:%s\n", os.Getenv("MCP_TASK_NAMESPACE"), apiBase())
	for _, root := range []string{p.WebPath, p.WorkerPath} {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == ".gregale" {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("release sources must not contain symlinks")
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(h, "%d:%s", len(rel), rel)
			f, err := openCustomerFile(path)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(h, ":%d:%d:", info.Mode(), info.Size())
			_, err = io.Copy(h, f)
			return err
		}); err != nil {
			return "", err
		}
	}
	if p.ReleasePolicy != "" {
		b, err := os.ReadFile(p.ReleasePolicy)
		if err != nil {
			return "", err
		}
		_, _ = h.Write(b)
		var policy struct {
			Roles []struct {
				Baseline string `json:"baseline"`
			} `json:"roles"`
		}
		if err := json.Unmarshal(b, &policy); err != nil {
			return "", err
		}
		root, err := os.OpenRoot(filepath.Dir(p.ReleasePolicy))
		if err != nil {
			return "", err
		}
		defer func() { _ = root.Close() }()
		for _, role := range policy.Roles {
			baseline, err := root.ReadFile(role.Baseline)
			if err != nil {
				return "", err
			}
			_, _ = h.Write(baseline)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func saveMCPNativeState(path string, s mcpNativeReleaseState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mcp-release-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func loadMCPNativeState(path, fingerprint string) (mcpNativeReleaseState, error) {
	s := mcpNativeReleaseState{Version: 1, Fingerprint: fingerprint, Stage: "prepared", PreviousDeployments: map[string]string{}, Parked: map[string]bool{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Fingerprint != fingerprint {
		return s, errors.New("release plan or sources changed; use the original artifact and journal to resume")
	}
	if s.PreviousDeployments == nil || s.Parked == nil {
		return s, errors.New("invalid release journal")
	}
	return s, nil
}
func cmdMCPTaskRelease(args []string) int {
	action := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	flags := newFlagSet("mcp-tasks-release", flag.ContinueOnError)
	planPath := flags.String("plan", "", "native release plan JSON")
	resume := flags.Bool("resume", false, "resume a healthy failed rollout (recover only)")
	statePath := flags.String("state", "", "persistent release journal outside source directories")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 || *planPath == "" || *statePath == "" || (action != "run" && action != "start" && action != "drain" && action != "restore-hook" && action != "status" && action != "recover" && action != "restore" && action != "quarantine" && action != "retire" && action != "retire-check" && action != "retire-hook") {
		return printErr("MCP release", errors.New("use release [run|status|recover|restore|quarantine|retire] --plan PATH --state PATH"))
	}
	p, err := readMCPNativePlan(*planPath)
	if err != nil {
		return printErr("MCP release plan", err)
	}
	absoluteState, err := filepath.Abs(*statePath)
	if err != nil {
		return printErr("MCP release state", err)
	}
	for _, root := range []string{p.WebPath, p.WorkerPath} {
		relative, e := filepath.Rel(root, absoluteState)
		if e != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
			return printErr("MCP release state", errors.New("store the journal outside both deployment sources"))
		}
	}
	fingerprint, err := mcpNativeFingerprint(p)
	if err != nil {
		return printErr("MCP release source", err)
	}
	s, err := loadMCPNativeState(absoluteState, fingerprint)
	if err != nil {
		return printErr("MCP release state", err)
	}
	if *resume && action != "recover" {
		return printErr("MCP recovery", errors.New("--resume requires recover"))
	}
	if action == "recover" {
		return recoverMCPNativeRelease(p, s, absoluteState, *planPath, *resume)
	}
	if action == "quarantine" || action == "retire" {
		return runMCPNativeRetirement(p, s, absoluteState, *planPath, action == "retire")
	}
	if action == "restore" {
		return runMCPNativeRestore(p, s, absoluteState, *planPath)
	}
	if action == "status" {
		return jsonOut(writeJSON(s))
	}
	if action == "run" {
		return runMCPNativeRelease(p, s, absoluteState, *planPath)
	}
	// Adapter entrypoints are used by the Node gate while it holds its namespace lock.
	if os.Getenv("MCP_TASK_RELEASE_INPUT") == "" {
		return printErr("MCP release adapter", errors.New("adapter requires gate input"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("MCP release", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	switch action {
	case "retire-check":
		err = checkMCPNativeRetirement(ctx, c, p, &s)
	case "retire-hook":
		err = retireMCPNativeWorker(ctx, c, p, &s, absoluteState)
	case "restore-hook":
		err = restoreMCPNativeWeb(ctx, c, p, &s, absoluteState)
	case "start":
		err = startMCPNativeRelease(ctx, c, p, &s, absoluteState)
	default:
		err = drainMCPNativeRelease(ctx, c, p, &s, absoluteState)
	}
	if err != nil {
		return printErr("MCP release adapter", err)
	}
	return jsonOut(writeJSON(map[string]any{"workerIDs": s.WorkerIDs, "stage": s.Stage}))
}
func runMCPNativeRelease(p mcpNativeReleasePlan, s mcpNativeReleaseState, state, plan string) int {
	if s.Stage == "restore_pending" || s.Stage == "web_restored" || s.Stage == "quarantined" || s.Stage == "retirement_pending" || s.Stage == "worker_retired" {
		return printErr("MCP release", errors.New("recovery or retirement started; resume that operation or create a new release instead"))
	}
	if s.Stage == "complete" {
		return jsonOut(writeJSON(s))
	}
	if s.PendingSubmission != "" {
		c, err := authedClient()
		if err != nil {
			return printErr("MCP release recovery", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds)*time.Second)
		err = reconcileMCPNativeSubmission(ctx, c, p, &s, state)
		cancel()
		if err != nil {
			return printErr("MCP release recovery", err)
		}
	}
	if _, err := os.Stat(state); os.IsNotExist(err) {
		body, _ := json.Marshal(s)
		f, err := os.OpenFile(state, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return printErr("MCP release journal", err)
		}
		_, err = f.Write(body)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return printErr("MCP release journal", err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		return printErr("MCP release", err)
	}
	plan, err = filepath.Abs(plan)
	if err != nil {
		return printErr("MCP release", err)
	}
	gatePlan := map[string]any{"timeoutMs": p.TimeoutSeconds * 1000, "checkpointPath": state + ".gate", "start": []string{binary, "--json", "mcp", "tasks", "release", "start", "--plan", plan, "--state", state}, "drain": []string{binary, "--json", "mcp", "tasks", "release", "drain", "--plan", plan, "--state", state}}
	b, _ := json.Marshal(gatePlan)
	f, err := os.CreateTemp("", "gregale-native-release-*.json")
	if err != nil {
		return printErr("MCP release", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return printErr("MCP release", err)
	}
	if err = f.Close(); err != nil {
		return printErr("MCP release plan", err)
	}
	// Each adapter and each wait has its own deadline; bound the entire invocation too.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds*5+120)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "tasks-release.js", f.Name())
	command.Dir = p.WorkerPath
	output, runErr := command.Output()
	var report struct {
		OK     bool   `json:"ok"`
		Stage  string `json:"stage"`
		Detail string `json:"detail,omitempty"`
	}
	if json.Unmarshal(output, &report) != nil {
		return printErr("MCP Task release", errors.New("gate failed; check Node dependencies and deployment bindings"))
	}
	if !report.OK {
		report.Detail = "Inspect the release journal and recorded deployments, then rerun with unchanged sources and plan"
	}
	if report.OK {
		s, err = loadMCPNativeState(state, s.Fingerprint)
		if err != nil {
			return printErr("MCP release journal", err)
		}
		s.Stage = "complete"
		if err = saveMCPNativeState(state, s); err != nil {
			return printErr("MCP release journal", err)
		}
	}
	if code := jsonOut(writeJSON(report)); code != 0 {
		return code
	}
	if runErr != nil || !report.OK {
		return 1
	}
	return 0
}

func mcpNativeObserverHealthy(ctx context.Context, c *Client, p mcpNativeReleasePlan) error {
	app, err := c.GetApp(ctx, p.ObserverApp)
	if err != nil {
		return err
	}
	if !isWorkerApp(app) || app.Manifest.WorkerReplicas == nil || app.Manifest.WorkerReplicas.Min < 1 {
		return errors.New("observer must be an always-on worker app")
	}
	instances, err := c.ListInstances(ctx, p.ObserverApp)
	if err != nil {
		return err
	}
	running := false
	for _, ins := range instances {
		if ins.State == "running" {
			running = true
		}
	}
	if !running {
		return errors.New("observer has no running instances")
	}
	metrics, err := c.GetAppCustomMetrics(ctx, p.ObserverMetricApp)
	if err != nil {
		return err
	}
	for _, m := range metrics.Metrics {
		if m.Name == "mcp_tasks_observer_heartbeat" && !m.Stale && m.Value == 1 {
			return nil
		}
	}
	return errors.New("observer heartbeat is missing or stale")
}
func mcpNativeWaitDeployment(ctx context.Context, c *Client, app, id string) error {
	for {
		dep, err := c.GetDeployment(ctx, id)
		if err != nil {
			return err
		}
		owner, err := c.GetApp(ctx, app)
		if err != nil {
			return err
		}
		if dep.AppID != owner.ID {
			return errors.New("journal deployment belongs to another app")
		}
		if dep.Status == statusLive {
			return nil
		}
		if dep.Status == "failed" || dep.Status == "cancelled" || dep.Status == "superseded" {
			return errors.New("recorded deployment cannot resume")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Reconciliation only reads deployment history; it never retries an uncertain POST.
func reconcileMCPNativeSubmission(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState, state string) error {
	kind := s.PendingSubmission
	if kind == "" {
		return nil
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(s.ReleaseID) || (kind != "web" && kind != "worker") {
		return errors.New("legacy or invalid pending submission requires manual reconciliation")
	}
	app := p.WorkerApp
	if kind == "web" {
		app = p.WebApp
	}
	owner, err := c.GetApp(ctx, app)
	if err != nil {
		return err
	}
	if owner.ID == "" {
		return errors.New("target app identity is missing")
	}
	deployments, err := c.ListAppDeploymentsAll(ctx, app)
	if err != nil {
		return err
	}
	id := ""
	reason := "mcp-release-" + s.ReleaseID + "-" + kind
	for _, dep := range deployments {
		if dep.Reason != reason {
			continue
		}
		if dep.AppID != owner.ID || dep.ID == "" || id != "" {
			return errors.New("release submission history is ambiguous or belongs to another app")
		}
		id = dep.ID
	}
	if id == "" {
		return errors.New("release submission is not yet visible in history; retry reconciliation later without resubmitting")
	}
	recorded := s.WorkerDeployment
	if kind == "web" {
		recorded = s.WebDeployment
	}
	if recorded != "" && recorded != id {
		return errors.New("submission history conflicts with the recorded candidate")
	}
	if kind == "web" {
		s.WebDeployment = id
	} else {
		s.WorkerDeployment = id
	}
	s.PendingSubmission = ""
	return saveMCPNativeState(state, *s)
}

func mcpNativeDeploy(ctx context.Context, c *Client, path, app string, web bool, s *mcpNativeReleaseState, state string) error {
	id := s.WorkerDeployment
	kind := "worker"
	if web {
		id = s.WebDeployment
		kind = "web"
	}
	if id != "" {
		return mcpNativeWaitDeployment(ctx, c, app, id)
	}
	if s.PendingSubmission != "" {
		return errors.New("deployment submission outcome is unknown; reconcile it before resuming")
	}
	if s.ReleaseID == "" {
		var entropy [16]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return err
		}
		s.ReleaseID = hex.EncodeToString(entropy[:])
	}
	s.PendingSubmission = kind
	if err := saveMCPNativeState(state, *s); err != nil {
		return err
	}
	args := []string{"--path", path, "--source=worktree", "--name", app, "--app", "--wait", "--timeout", "600", "--reason", "mcp-release-" + s.ReleaseID + "-" + kind, "--idempotency-key", "mcp-release-" + s.ReleaseID + "-" + kind}
	if web {
		args = append(args, "--no-traffic")
	}
	var journalErr error
	code := quietMCPDeployContext(ctx, args, deployExecution{onQueued: func(dep api.DeploymentResponse) {
		if web {
			s.WebDeployment = dep.ID
		} else {
			s.WorkerDeployment = dep.ID
		}
		s.PendingSubmission = ""
		journalErr = saveMCPNativeState(state, *s)
	}})
	if journalErr != nil {
		return journalErr
	}
	if code != 0 {
		return errors.New("deployment failed; inspect the journal and recorded deployment before resuming")
	}
	return nil
}

var mcpWorkerUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func mcpNativeWorkerLogs(reader io.Reader, instances []api.InstanceResponse, dep string, requireFence ...bool) ([]string, error) {
	wanted := map[string]bool{}
	for _, ins := range instances {
		if ins.DeploymentID == dep && ins.State == "running" {
			wanted[ins.ID] = true
		}
	}
	found := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(reader, 4<<20))
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "data: ")
		if line == scanner.Text() {
			continue
		}
		var envelope struct {
			Instance string `json:"instance"`
			Line     string `json:"line"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil || !wanted[envelope.Instance] {
			continue
		}
		var event struct {
			Event      string `json:"event"`
			WorkerID   string `json:"workerID"`
			ClaimFence bool   `json:"claimFence"`
		}
		if json.Unmarshal([]byte(envelope.Line), &event) == nil && event.Event == "mcp_task_worker_started" && mcpWorkerUUID.MatchString(event.WorkerID) && (len(requireFence) == 0 || !requireFence[0] || event.ClaimFence) {
			if len(requireFence) > 0 && requireFence[0] && found[envelope.Instance] != "" && found[envelope.Instance] != event.WorkerID {
				return nil, errors.New("candidate instance has multiple startup worker IDs; refusing quarantine")
			}
			found[envelope.Instance] = event.WorkerID
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(wanted) == 0 || len(found) != len(wanted) {
		return nil, errors.New("current candidate instances lack worker startup IDs")
	}
	ids := []string{}
	for _, id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
func mcpNativeWorkerIDs(ctx context.Context, c *Client, app, dep string, requireFence ...bool) ([]string, error) {
	instances, err := c.ListInstances(ctx, app)
	if err != nil {
		return nil, err
	}
	body, err := c.StreamAppLogs(ctx, app, dep, false, api.LogFilter{Grep: "mcp_task_worker_started"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return mcpNativeWorkerLogs(body, instances, dep, requireFence...)
}
func mcpNativeVerifyEndpoint(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState) error {
	cfg, err := mcphosting.Load(p.WebPath)
	if err != nil {
		return err
	}
	token, err := mcpToken(p.TokenEnv)
	if err != nil {
		return err
	}
	if cfg.Auth.Mode == "external-oauth" && token == "" {
		return errors.New("external OAuth verification requires token_env")
	}
	current, e := mcpServingDeployment(ctx, c, p.WebApp, "")
	if e != nil {
		return e
	}
	if current == s.WebDeployment && s.WebDeployment != "" {
		s.Promoted = true
	}
	_, candidate, serving, err := prepareMCPCandidate(ctx, c, p.WebApp, s.WebDeployment, cfg)
	// A promoted candidate is verified using its deployment URL on recovery.
	if s.Promoted {
		dep, e := c.GetDeployment(ctx, s.WebDeployment)
		if e != nil {
			return e
		}
		app, e := c.GetApp(ctx, p.WebApp)
		if e != nil {
			return e
		}
		current, e := mcpServingDeployment(ctx, c, p.WebApp, "")
		if e != nil {
			return e
		}
		if dep.AppID != app.ID || current != s.WebDeployment {
			return errors.New("serving deployment changed after promotion")
		}
		if app.MaintenanceMode || app.RequireAuthn || app.PublicAuth.Mode != api.AppPublicAuthModeOpen || !app.StreamingEnabled {
			return errors.New("promoted MCP ingress is unavailable")
		}
		canonical, e := cfg.URL(canonicalAppURL(app))
		if e != nil {
			return e
		}
		if cfg.Auth.Mode == "external-oauth" && cfg.Auth.Resource != canonical {
			return errors.New("promoted OAuth resource differs from canonical endpoint")
		}
		preview, e := c.GetDeploymentURL(ctx, s.WebDeployment)
		if e != nil {
			return e
		}
		if !preview.Alive {
			return errors.New("candidate preview unavailable")
		}
		candidate, e = cfg.URL(canonicalAppURL(app))
		if e != nil {
			return e
		}
		err = nil
	} else if err == nil && s.ServingCaptured && serving != s.ServingDeployment {
		return errors.New("serving deployment changed during release")
	}
	if err != nil {
		return err
	}
	probe, err := mcphosting.NewClient(candidate, token, mcphosting.ProtocolVersion)
	if err != nil {
		return err
	}
	if cfg.Auth.Mode == "external-oauth" {
		probe.ExpectedAuth = &cfg.Auth
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report := mcphosting.Doctor(probeCtx, probe, cfg.Legacy, "", nil)
	if !report.OK {
		return errors.New("candidate MCP endpoint failed verification")
	}
	roles, err := loadMCPReleasePolicy(p.ReleasePolicy)
	if err != nil {
		return err
	}
	_, err = verifyMCPRoles(probeCtx, candidate, roles)
	return err
}

func startMCPNativeRelease(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState, state string) error {
	cfgWeb, err := mcphosting.Load(p.WebPath)
	if err != nil {
		return err
	}
	token, err := mcpToken(p.TokenEnv)
	if err != nil {
		return err
	}
	if cfgWeb.Auth.Mode == "external-oauth" && token == "" {
		return errors.New("external OAuth verification requires token_env")
	}
	if _, err := loadMCPReleasePolicy(p.ReleasePolicy); err != nil {
		return err
	}
	account, err := c.Whoami(ctx)
	if err != nil {
		return err
	}
	if !api.Plan(account.Plan).StreamingEnabled() {
		return errors.New("MCP release requires a streaming-enabled plan")
	}
	if err := mcpNativeObserverHealthy(ctx, c, p); err != nil {
		return err
	}
	candidate, err := c.GetApp(ctx, p.WorkerApp)
	if err != nil {
		return err
	}
	if !isWorkerApp(candidate) || candidate.Manifest.WorkerReplicas == nil || candidate.Manifest.WorkerReplicas.Min < 1 {
		return errors.New("candidate must be a worker app with at least one replica")
	}
	if !s.ServingCaptured {
		instances, err := c.ListInstances(ctx, p.WorkerApp)
		if err != nil {
			return err
		}
		if len(instances) != 0 {
			return errors.New("candidate worker app must have no existing instances")
		}
		deployments, err := c.ListAppDeploymentsAll(ctx, p.WorkerApp)
		if err != nil {
			return err
		}
		for _, dep := range deployments {
			if dep.Status == statusLive {
				return errors.New("candidate worker app must have no live deployment")
			}
		}
		serving, err := mcpServingDeployment(ctx, c, p.WebApp, "")
		if err != nil {
			return err
		}
		s.ServingDeployment = serving
		var input struct {
			Previous []string `json:"previousWorkerIDs"`
		}
		if json.Unmarshal([]byte(os.Getenv("MCP_TASK_RELEASE_INPUT")), &input) != nil {
			return errors.New("invalid gate input")
		}
		covered := map[string]bool{}
		cfg, err := mcphosting.Load(p.WorkerPath)
		if err != nil {
			return err
		}
		if cfg.Tasks == nil || cfg.Tasks.Enabled == nil || !*cfg.Tasks.Enabled {
			return errors.New("candidate requires enabled Tasks")
		}
		shutdownDeadline := time.Duration(cfg.Tasks.ShutdownTimeoutMS) * time.Millisecond
		if shutdownDeadline == 0 {
			shutdownDeadline = 30 * time.Second
		}
		for _, slug := range p.PreviousWorkerApps {
			app, err := c.GetApp(ctx, slug)
			if err != nil {
				return err
			}
			if !isWorkerApp(app) || app.ID == candidate.ID {
				return errors.New("previous workers must be distinct worker apps")
			}
			if app.Manifest.StopGracePeriod <= shutdownDeadline {
				return errors.New("previous worker stop grace must exceed the Task shutdown deadline")
			}
			dep, err := c.GetLatestAppDeployment(ctx, slug)
			if err != nil {
				return err
			}
			if dep.AppID != app.ID {
				return errors.New("previous deployment ownership mismatch")
			}
			ids, err := mcpNativeWorkerIDs(ctx, c, slug, dep.ID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				covered[id] = true
			}
			s.PreviousDeployments[slug] = dep.ID
		}
		for _, id := range input.Previous {
			if !covered[id] {
				return errors.New("namespace contains workers outside previous_worker_apps")
			}
		}
		s.ServingCaptured = true
		if err := saveMCPNativeState(state, *s); err != nil {
			return err
		}
	}
	s.Stage = "deploying_worker"
	if err := saveMCPNativeState(state, *s); err != nil {
		return err
	}
	if err := mcpNativeDeploy(ctx, c, p.WorkerPath, p.WorkerApp, false, s, state); err != nil {
		return err
	}
	s.Stage = "deploying_web"
	if err := saveMCPNativeState(state, *s); err != nil {
		return err
	}
	if err := mcpNativeDeploy(ctx, c, p.WebPath, p.WebApp, true, s, state); err != nil {
		return err
	}
	if err := mcpNativeVerifyEndpoint(ctx, c, p, s); err != nil {
		return err
	}
	if err := mcpNativeObserverHealthy(ctx, c, p); err != nil {
		return err
	}
	for {
		ids, err := mcpNativeWorkerIDs(ctx, c, p.WorkerApp, s.WorkerDeployment)
		if err == nil {
			s.WorkerIDs = ids
			s.Stage = "replacements_started"
			return saveMCPNativeState(state, *s)
		}
		select {
		case <-ctx.Done():
			return errors.New("candidate worker startup IDs unavailable before deadline")
		case <-time.After(time.Second):
		}
	}
}

var mcpNativeCheckWorkers = checkMCPNativeWorkers

func checkMCPNativeWorkers(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState) error {
	current, err := mcpNativeWorkerIDs(ctx, c, p.WorkerApp, s.WorkerDeployment)
	if err != nil {
		return err
	}
	for _, expected := range s.WorkerIDs {
		found := false
		for _, id := range current {
			if id == expected {
				found = true
			}
		}
		if !found {
			return errors.New("replacement instance changed during release")
		}
	}
	input, _ := json.Marshal(map[string]any{"replacementWorkerIDs": s.WorkerIDs})
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, "node", "tasks-release-readiness.js")
	command.Dir = p.WorkerPath
	env := []string{}
	for _, binding := range os.Environ() {
		if !strings.HasPrefix(binding, "MCP_TASK_RELEASE_INPUT=") && !strings.HasPrefix(binding, "MCP_TASK_MIGRATION_DATABASE_URL=") {
			env = append(env, binding)
		}
	}
	command.Env = append(env, "MCP_TASK_RELEASE_INPUT="+string(input))
	output, err := command.Output()
	var report struct {
		OK bool `json:"ok"`
	}
	if err != nil || json.Unmarshal(output, &report) != nil || !report.OK {
		return errors.New("replacement database heartbeats are not ready")
	}
	return nil
}

func drainMCPNativeRelease(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState, state string) error {
	var input struct {
		Replacements []string `json:"replacementWorkerIDs"`
	}
	if json.Unmarshal([]byte(os.Getenv("MCP_TASK_RELEASE_INPUT")), &input) != nil || len(input.Replacements) == 0 || strings.Join(input.Replacements, ",") != strings.Join(s.WorkerIDs, ",") {
		return errors.New("replacement IDs differ from release journal")
	}
	if err := mcpNativeVerifyEndpoint(ctx, c, p, s); err != nil {
		return err
	}
	if err := mcpNativeObserverHealthy(ctx, c, p); err != nil {
		return err
	}
	// Re-read exact generations before the first irreversible traffic/drain action.
	for _, slug := range p.PreviousWorkerApps {
		dep, err := c.GetLatestAppDeployment(ctx, slug)
		if err != nil {
			return err
		}
		if dep.ID != s.PreviousDeployments[slug] {
			return errors.New("previous worker deployment changed; refusing app-wide drain")
		}
	}
	if err := mcpNativeCheckWorkers(ctx, c, p, s); err != nil {
		return err
	}
	if !s.Promoted {
		_, err := c.PatchDeploymentTrafficIfServing(ctx, s.WebDeployment, 100, s.ServingDeployment)
		if err != nil {
			// The compare-and-swap may have committed before the client lost its response.
			current, e := mcpServingDeployment(ctx, c, p.WebApp, "")
			if e != nil || current != s.WebDeployment {
				return err
			}
		}
		s.Promoted = true
		s.Stage = "web_promoted"
		if err := saveMCPNativeState(state, *s); err != nil {
			return err
		}
	}
	if err := mcpNativeVerifyEndpoint(ctx, c, p, s); err != nil {
		return err
	}
	for _, slug := range p.PreviousWorkerApps {
		// Park is idempotent and waits for the app's zero-live boundary. Retry a lost
		// response using the same app/deployment journal rather than deploying again.
		if s.Parked[slug] {
			continue
		}
		if err := mcpNativeObserverHealthy(ctx, c, p); err != nil {
			return err
		}
		dep, err := c.GetLatestAppDeployment(ctx, slug)
		if err != nil {
			return err
		}
		if dep.ID != s.PreviousDeployments[slug] {
			return errors.New("previous worker deployment changed before park")
		}
		if err := mcpNativeCheckWorkers(ctx, c, p, s); err != nil {
			return err
		}
		if err := c.ParkIfDeployment(ctx, slug, s.PreviousDeployments[slug]); err != nil {
			return err
		}
		s.Parked[slug] = true
		s.Stage = "draining_previous"
		if err := saveMCPNativeState(state, *s); err != nil {
			return err
		}
	}
	s.Stage = "previous_parked"
	return saveMCPNativeState(state, *s)
}
