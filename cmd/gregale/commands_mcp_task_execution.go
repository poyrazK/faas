package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// This file is local owner setup. Neither its path nor any value is accepted
// from an agent tool or included in an API record or tool result.
type mcpTaskExecutionProfile struct {
	APIBase            string            `json:"api_base"`
	AccountID          string            `json:"account_id"`
	IntentID           string            `json:"intent_id"`
	OperationID        string            `json:"operation_id"`
	Plan               string            `json:"plan"`
	Salt               string            `json:"salt"`
	DependenciesSHA256 string            `json:"dependencies_sha256"`
	Env                map[string]string `json:"env"`
}

func mcpTaskExecutionDir(account, id string) (string, error) {
	if !toolpublication.ValidID(id) {
		return "", errors.New("invalid Task intent identity")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	identity := toolpublication.Digest([]byte(apiBase() + "\n" + account))
	dir := filepath.Join(base, "gregale", "task-executions", identity, id)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("task execution directory must be private")
	}
	return dir, nil
}

func readMCPTaskPrivate(path string, out any) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > api.TaskToolReleaseIntentMaxBytes {
		return errors.New("task execution requires a bounded private regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return errors.New("invalid private Task execution configuration")
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("private Task configuration must contain one JSON value")
	}
	return nil
}

func writeMCPTaskPrivate(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > api.TaskToolReleaseIntentMaxBytes {
		return errors.New("private Task execution file exceeds its bound")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if err != nil {
		return err
	}
	dir, err := openCustomerDirectoryForSync(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func mcpTaskExecutionEntropy() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

func (p mcpTaskExecutionProfile) digest() string {
	b, _ := json.Marshal(p)
	return toolpublication.Digest(b)
}

func mcpTaskExecutionEnv(p mcpTaskExecutionProfile) (func(), error) {
	plan, err := readMCPNativePlan(p.Plan)
	if err != nil {
		return nil, err
	}
	dir, err := mcpTaskExecutionDir(p.AccountID, p.IntentID)
	if err != nil {
		return nil, err
	}
	for _, root := range []string{plan.WebPath, plan.WorkerPath} {
		relative, e := filepath.Rel(root, dir)
		if e != nil || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil, errors.New("private Task bindings must be outside deployment sources")
		}
	}
	cfg, err := mcphosting.Load(plan.WorkerPath)
	if err != nil || cfg.Tasks == nil {
		return nil, errors.New("task worker configuration unavailable")
	}
	allowed := map[string]bool{"MCP_TASK_NAMESPACE": true, "MCP_TASK_MIGRATION_DATABASE_URL": true, "MCP_TASK_OBSERVER_DATABASE_URL": true}
	for _, name := range []string{cfg.Tasks.DatabaseURLEnv, cfg.Tasks.OwnerKeyEnv, cfg.Tasks.EncryptionKeysEnv, cfg.Tasks.NamespaceEnv, plan.TokenEnv} {
		if name != "" {
			allowed[name] = true
		}
	}
	for _, key := range cfg.Tasks.PreviousOwnerKeys {
		allowed[key.Env] = true
	}
	roles, err := loadMCPReleasePolicyWithTokens(plan.ReleasePolicy, false)
	if err != nil {
		return nil, err
	}
	for _, role := range roles {
		if role.TokenEnv != "" {
			allowed[role.TokenEnv] = true
		}
	}
	if len(p.Env) == 0 || len(p.Env) > 64 {
		return nil, errors.New("invalid Task binding profile")
	}
	for name, value := range p.Env {
		if !allowed[name] || value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(name, "FAAS_") || strings.HasPrefix(name, "GREGALE_") ||
			name == "PATH" || strings.HasPrefix(name, "NODE_") || strings.HasPrefix(name, "LD_") || name == "HOME" || name == "XDG_CONFIG_HOME" || name == "SSLKEYLOGFILE" {
			return nil, errors.New("task binding profile contains an unapproved environment setting")
		}
	}
	for name := range allowed {
		if p.Env[name] == "" {
			return nil, errors.New("task binding profile is incomplete")
		}
	}
	old := map[string]*string{}
	for name, value := range p.Env {
		if prev, ok := os.LookupEnv(name); ok {
			saved := prev
			old[name] = &saved
		} else {
			old[name] = nil
		}
		_ = os.Setenv(name, value)
	}
	return func() {
		for name, value := range old {
			if value == nil {
				_ = os.Unsetenv(name)
			} else {
				_ = os.Setenv(name, *value)
			}
		}
	}, nil
}

func cmdMCPTaskExecution(args []string) int {
	if len(args) == 0 {
		return printErr("Task execution", errors.New("use execution authorize|apply|get|guard|inspect|plan-recovery"))
	}
	action := args[0]
	if action == "quarantine" || strings.HasPrefix(action, "quarantine-") {
		return cmdMCPTaskQuarantine(args)
	}
	if action == "restore" || action == "restore-hook" {
		return cmdMCPTaskRestore(args)
	}
	if strings.HasPrefix(action, "recovery-") {
		return cmdMCPTaskRecoveryOwner(args)
	}
	flags := newFlagSet("mcp-task-execution", flag.ContinueOnError)
	id := flags.String("id", "", "reviewed intent identity")
	op := flags.String("operation-id", "", "one immutable owner-selected execution identity")
	plan := flags.String("plan", "", "owner-reviewed native plan")
	bindings := flags.String("bindings-file", "", "private owner binding JSON file")
	digest := flags.String("intent-sha256", "", "exact owner-reviewed intent digest")
	allow := flags.Bool("allow-tool-execution", false, "authorize reviewed code, migrations and native release effects")
	if flags.Parse(args[1:]) != nil {
		return 1
	}
	readOnly := action == "get" || action == "inspect" || action == "plan-recovery"
	if flags.NArg() != 0 || !toolpublication.ValidID(*id) || (action != "authorize" && action != "apply" && action != "get" && action != "guard" && action != "inspect" && action != "plan-recovery") ||
		(!readOnly && !toolpublication.ValidID(*op)) || (action == "authorize" && (*plan == "" || *bindings == "" || !toolpublication.ValidID(*digest) || !*allow)) ||
		(action != "authorize" && (*plan != "" || *bindings != "" || *digest != "")) || (action == "apply" && !*allow) {
		return printErr("Task execution", errors.New("require an exact intent/operation; owner authorization additionally requires plan, private bindings, digest and explicit execution consent"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Task execution", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.TaskToolReleaseApprovalTTL)
	defer cancel()
	if action == "inspect" || action == "plan-recovery" {
		out, err := tasktoolrelease.InspectRecovery(ctx, c, *id, action == "plan-recovery", time.Now())
		if err != nil {
			return printErr("Task recovery", err)
		}
		return jsonOut(writeJSON(out))
	}
	record, err := c.GetTaskToolReleaseIntent(ctx, *id)
	if err != nil {
		return printErr("Task execution", err)
	}
	if action == "get" {
		return jsonOut(writeJSON(record))
	}
	if action == "authorize" {
		return authorizeMCPTaskExecution(ctx, c, record, *op, *plan, *bindings, *digest)
	}
	if record.Execution == nil || record.Execution.OperationID != *op {
		return printErr("Task execution", errors.New("no matching owner execution authorization"))
	}
	// Repeats inspect the durable native claim, even if local files disappeared.
	if action == "apply" && record.Execution.State != "authorized" {
		return jsonOut(writeJSON(record))
	}
	p, dir, err := loadMCPTaskExecutionProfile(record)
	if err != nil {
		return printErr("Task execution", errors.New("private owner execution profile is unavailable or changed"))
	}
	restore, err := mcpTaskExecutionEnv(p)
	if err != nil {
		return printErr("Task execution", err)
	}
	defer restore()
	if action == "guard" {
		if err = guardMCPTaskExecution(ctx, c, record, p, dir); err != nil {
			return printErr("Task execution guard", errors.New("execution authority or reviewed inputs changed"))
		}
		return jsonOut(writeJSON(map[string]bool{"authorized": true}))
	}
	return applyMCPTaskExecution(ctx, c, record, p, dir)
}

func authorizeMCPTaskExecution(ctx context.Context, c *Client, record api.TaskToolReleaseIntentRecord, operation, plan, bindings, digest string) int {
	if record.IntentSHA256 != digest || record.Status != "approved" {
		return printErr("Task execution", errors.New("an exact current owner-approved review is required"))
	}
	path, err := filepath.Abs(plan)
	if err != nil {
		return printErr("Task execution", err)
	}
	native, err := readMCPNativePlan(path)
	if err != nil {
		return printErr("Task execution", err)
	}
	privateBindings, err := filepath.Abs(bindings)
	if err != nil {
		return printErr("Task execution", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(privateBindings))
	if err != nil {
		return printErr("Task execution", err)
	}
	privateBindings = filepath.Join(parent, filepath.Base(privateBindings))
	for _, root := range []string{native.WebPath, native.WorkerPath} {
		relative, e := filepath.Rel(root, privateBindings)
		if e != nil || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return printErr("Task execution", errors.New("owner binding input must stay outside deployment sources"))
		}
	}
	env := map[string]string{}
	if err = readMCPTaskPrivate(privateBindings, &env); err != nil {
		return printErr("Task execution", err)
	}
	dir, err := mcpTaskExecutionDir(record.AccountID, record.ID)
	if err != nil {
		return printErr("Task execution", err)
	}
	dependencies, err := mcpTaskExecutionDependencies(path)
	if err != nil {
		return printErr("Task execution", err)
	}
	p := mcpTaskExecutionProfile{APIBase: apiBase(), AccountID: record.AccountID, IntentID: record.ID, OperationID: operation, Plan: path, Env: env, DependenciesSHA256: dependencies}
	if _, e := os.Lstat(filepath.Join(dir, "profile.json")); e == nil {
		var existing mcpTaskExecutionProfile
		if readMCPTaskPrivate(filepath.Join(dir, "profile.json"), &existing) != nil || existing.APIBase != p.APIBase || existing.AccountID != p.AccountID || existing.IntentID != p.IntentID || existing.OperationID != p.OperationID || existing.Plan != p.Plan || existing.DependenciesSHA256 != p.DependenciesSHA256 || !reflect.DeepEqual(existing.Env, p.Env) {
			return printErr("Task execution", errors.New("owner execution profile is immutable"))
		}
		p = existing
	} else {
		p.Salt, err = mcpTaskExecutionEntropy()
		if err != nil {
			return printErr("Task execution", err)
		}
	}
	restore, err := mcpTaskExecutionEnv(p)
	if err != nil {
		return printErr("Task execution", err)
	}
	defer restore()
	review, err := reviewMCPTaskRelease(p.Plan)
	if err != nil || review.Fingerprint != record.Intent.ReleaseFingerprint {
		return printErr("Task execution", errors.New("source, API or namespace differs from the reviewed intent"))
	}
	if _, e := os.Lstat(filepath.Join(dir, "profile.json")); os.IsNotExist(e) {
		if err = writeMCPTaskPrivate(filepath.Join(dir, "profile.json"), p); err != nil {
			return printErr("Task execution", err)
		}
	}
	out, err := c.DecideTaskToolReleaseIntent(ctx, record.ID, api.TaskToolReleaseIntentDecision{Decision: "authorize", IntentSHA256: digest, ReleaseFingerprint: review.Fingerprint,
		Execution: &api.TaskToolReleaseExecutionRequest{Action: "authorize", OperationID: operation, BindingsSHA256: p.digest()}})
	if err != nil {
		return printErr("Task execution authorization", err)
	}
	return jsonOut(writeJSON(out))
}

func loadMCPTaskExecutionProfile(record api.TaskToolReleaseIntentRecord) (mcpTaskExecutionProfile, string, error) {
	var p mcpTaskExecutionProfile
	dir, err := mcpTaskExecutionDir(record.AccountID, record.ID)
	if err != nil {
		return p, dir, err
	}
	err = readMCPTaskPrivate(filepath.Join(dir, "profile.json"), &p)
	if err != nil || record.Execution == nil || p.APIBase != apiBase() || p.AccountID != record.AccountID || p.IntentID != record.ID || p.OperationID != record.Execution.OperationID || !toolpublication.ValidID(p.Salt) || p.digest() != record.Execution.BindingsSHA256 {
		return p, dir, errors.New("owner binding profile changed")
	}
	dependencies, err := mcpTaskExecutionDependencies(p.Plan)
	if err != nil || dependencies != p.DependenciesSHA256 {
		return p, dir, errors.New("owner-installed Task dependencies changed")
	}
	return p, dir, nil
}

func changeMCPTaskExecution(ctx context.Context, c *Client, record api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, token, action string) (api.TaskToolReleaseIntentRecord, error) {
	review, err := reviewMCPTaskRelease(p.Plan)
	if err != nil {
		return record, err
	}
	return c.CheckTaskToolReleaseIntent(ctx, record.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: review.Fingerprint,
		Execution: &api.TaskToolReleaseExecutionRequest{Action: action, FenceVersion: 1, OperationID: p.OperationID, BindingsSHA256: p.digest(), ClaimToken: token}})
}

func guardMCPTaskExecution(ctx context.Context, c *Client, record api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir string, stage ...string) error {
	var token string
	if err := readMCPTaskPrivate(filepath.Join(dir, "claim.json"), &token); err != nil {
		return err
	}
	review, err := reviewMCPTaskRelease(p.Plan)
	if err != nil {
		return err
	}
	request := &api.TaskToolReleaseExecutionRequest{Action: "guard", OperationID: p.OperationID, BindingsSHA256: p.digest(), ClaimToken: token}
	if len(stage) > 0 {
		request.Stage = stage[0]
	}
	out, err := c.CheckTaskToolReleaseIntent(ctx, record.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: review.Fingerprint, Execution: request})
	if err != nil || out.Status != "approved" || out.Execution == nil || out.Execution.State != "running" || out.Execution.ClaimSHA256 != toolpublication.Digest([]byte(token)) {
		return errors.New("task execution is no longer authorized")
	}
	return nil
}

func applyMCPTaskExecution(ctx context.Context, c *Client, record api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, dir string) int {
	state := filepath.Join(dir, "release.json")
	release, err := ownMCPJournal(state, false)
	if err != nil {
		return printErr("Task execution", err)
	}
	defer release()
	token, err := mcpTaskExecutionEntropy()
	if err != nil {
		return printErr("Task execution", err)
	}
	if err = writeMCPTaskPrivate(filepath.Join(dir, "claim.json"), token); err != nil {
		return printErr("Task execution", errors.New("a prior local claim requires owner reconciliation"))
	}
	claimed, err := changeMCPTaskExecution(ctx, c, record, p, token, "claim")
	if err != nil {
		return printErr("Task execution claim", err)
	}
	if claimed.Status != "approved" || claimed.Execution == nil || claimed.Execution.State != "running" || claimed.Execution.FenceVersion != 1 || claimed.Execution.ClaimSHA256 != toolpublication.Digest([]byte(token)) {
		return jsonOut(writeJSON(claimed))
	}
	// From this point all uncertain outcomes remain claimed. Never auto-retry.
	complete := false
	settled := true
	defer func() { exitMCPTaskExecutor(ctx, c, record, p, token, settled) }()
	defer func() {
		if !complete {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = c.CheckTaskToolReleaseIntent(cleanup, record.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: record.Intent.ReleaseFingerprint, Execution: &api.TaskToolReleaseExecutionRequest{Action: "reconcile", OperationID: p.OperationID, BindingsSHA256: p.digest(), ClaimToken: token}})
		}
	}()
	plan, err := readMCPNativePlan(p.Plan)
	if err != nil {
		return printErr("Task execution", err)
	}
	if err = guardMCPTaskExecution(ctx, c, record, p, dir); err != nil {
		return printErr("Task execution", errors.New("execution authority changed before preflight"))
	}
	if err = verifyMCPTaskExecutionBindings(ctx, plan, p); err != nil {
		return printErr("Task execution", errors.New("live Task database-role verification failed; owner reconciliation required"))
	}
	fingerprint, err := mcpNativeFingerprint(plan)
	if err != nil || fingerprint != record.Intent.ReleaseFingerprint {
		return printErr("Task execution", errors.New("reviewed source changed"))
	}
	s, err := loadMCPNativeState(state, fingerprint)
	if err != nil || s.journalExists {
		return printErr("Task execution", errors.New("prior native journal requires owner reconciliation"))
	}
	s.TaskIntentID, s.TaskOperationID = record.ID, p.OperationID
	s.ReleaseID = tasktoolrelease.ExecutionReleaseID(record)
	if err = saveMCPNativeStateContext(ctx, state, &s); err != nil {
		return printErr("Task execution", err)
	}
	if err = guardMCPTaskExecution(ctx, c, record, p, dir); err != nil {
		return printErr("Task execution", errors.New("execution authority changed before release"))
	}
	// Adapter output remains private. Return only the native credential-free receipt.
	s.quiet = true
	settled = false
	s.executorSettled = &settled
	if code := runMCPNativeReleaseContext(ctx, plan, s, state, p.Plan); code != 0 {
		return printErr("Task execution", errors.New("release stopped; inspect the durable operation and reconcile its native journal"))
	}
	s, err = loadMCPNativeState(state, fingerprint)
	if err != nil || s.Stage != "complete" || s.WebDeployment == "" || s.WorkerDeployment == "" || !s.Promoted || s.PendingSubmission != "" {
		return printErr("Task execution", errors.New("native release did not record verified completion"))
	}
	for _, previous := range plan.PreviousWorkerApps {
		if !s.Parked[previous] {
			return printErr("Task execution", errors.New("native worker retirement is incomplete"))
		}
	}
	out, err := changeMCPTaskExecution(ctx, c, record, p, token, "complete")
	if err != nil || out.Execution == nil || out.Execution.State != "completed" {
		return printErr("Task execution", errors.New("completion requires owner reconciliation"))
	}
	complete = true
	return jsonOut(writeJSON(out))
}

func guardMCPNativeTaskExecution(ctx context.Context, c *Client, s *mcpNativeReleaseState) error {
	if s.TaskIntentID == "" && s.TaskOperationID == "" {
		return nil
	}
	if !toolpublication.ValidID(s.TaskIntentID) || !toolpublication.ValidID(s.TaskOperationID) {
		return errors.New("invalid Task execution journal identity")
	}
	record, err := c.GetTaskToolReleaseIntent(ctx, s.TaskIntentID)
	if err != nil {
		return err
	}
	if record.Execution == nil || record.Execution.OperationID != s.TaskOperationID {
		return errors.New("task execution identity changed")
	}
	p, dir, err := loadMCPTaskExecutionProfile(record)
	if err != nil {
		return err
	}
	return guardMCPTaskExecution(ctx, c, record, p, dir, s.Stage)
}

// Keep ambient Node loaders, PostgreSQL defaults and unrelated credentials out
// of the reviewed adapter. API authority is only needed by its fixed children.
func mcpTaskExecutionChildEnv(p mcpTaskExecutionProfile, apiAuthority bool) []string {
	names := []string{"PATH", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TMPDIR", "SYSTEMROOT", "SSL_CERT_FILE", "SSL_CERT_DIR", mcpJournalOwnerEnv}
	if apiAuthority {
		names = append(names, "FAAS_TOKEN", "FAAS_API", mcpTaskFenceEnv, mcpTaskFenceAPIEnv)
	}
	out := []string{}
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+value)
		}
	}
	for name, value := range p.Env {
		out = append(out, name+"="+value)
	}
	return out
}
