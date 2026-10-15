package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type automationLiveSuitePlan struct {
	App          string                                     `json:"app"`
	AppID        string                                     `json:"app_id"`
	Name         string                                     `json:"name"`
	EndpointHash string                                     `json:"endpoint_hash"`
	Key          string                                     `json:"idempotency_key"`
	Target       api.StartAutomationLiveVerificationRequest `json:"target"`
	Cases        []string                                   `json:"cases"`
	CreatedAt    time.Time                                  `json:"created_at"`
	Shared       bool                                       `json:"shared,omitempty"`
}

type automationLiveSuiteCase struct {
	Name         string                          `json:"name"`
	Key          string                          `json:"key"`
	AttemptedAt  *time.Time                      `json:"attempted_at,omitempty"`
	RunID        string                          `json:"run_id,omitempty"`
	Outcome      string                          `json:"outcome"`
	ObservedAt   *time.Time                      `json:"observed_at,omitempty"`
	Verification *api.AutomationLiveVerification `json:"verification,omitempty"`
}

type automationLiveSuiteJournal struct {
	SchemaVersion int                       `json:"schema_version"`
	Revision      uint64                    `json:"revision"`
	Plan          automationLiveSuitePlan   `json:"plan"`
	Fingerprint   string                    `json:"fingerprint"`
	Cases         []automationLiveSuiteCase `json:"cases"`
	diskHash      string
}

type automationLiveSuiteOptions struct {
	plan              automationLiveSuitePlan
	path, resume      string
	timeout, interval time.Duration
}

func cmdAutomationsLiveSuite(args []string) int {
	o, err := parseAutomationLiveSuite(args)
	if err != nil {
		return printErr("Invalid live suite options", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	lock, err := lockAutomationLiveSuite(o.path)
	if err != nil {
		return printErr("Could not own live suite journal", err)
	}
	defer func() { _ = lock.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	j, err := prepareAutomationLiveSuite(ctx, client, o)
	if err != nil {
		if ctx.Err() != nil {
			return printErr("Suite preparation stopped", &exitErr{msg: "Suite preparation stopped before execution; keep any existing journal.", code: liveSuiteErrorCode(ctx, ctx.Err())})
		}
		return printErr("Could not prepare live suite", err)
	}
	return executeAutomationLiveSuite(ctx, client, o, &j)
}

func parseAutomationLiveSuite(args []string) (automationLiveSuiteOptions, error) {
	var o automationLiveSuiteOptions
	fs := newFlagSet("automations live suite", flag.ContinueOnError)
	fs.StringVar(&o.plan.App, "app", "", "app slug for a new suite")
	fs.StringVar(&o.plan.Name, "name", "", "automation name for a new suite")
	fs.StringVar(&o.plan.Key, "idempotency-key", "", "stable suite key for a new suite")
	fs.Int64Var(&o.plan.Target.ExpectedVersion, "expected-version", 0, "saved automation version")
	fs.Int64Var(&o.plan.Target.ExpectedPublishedVersion, "expected-published-version", 0, "published revision")
	fs.StringVar(&o.plan.Target.ExpectedDeploymentID, "expected-deployment-id", "", "live default deployment UUID")
	fs.StringVar(&o.path, "journal", "", "new private journal file")
	fs.StringVar(&o.resume, "resume", "", "existing suite journal to continue")
	fs.BoolVar(&o.plan.Shared, "share", false, "retain this new suite and its run links in shared dashboard history")
	fs.BoolVar(&o.plan.Target.ConfirmRealActions, "confirm-real-actions", false, "confirm execution of remaining selected cases")
	fs.DurationVar(&o.timeout, "timeout", api.AutomationLiveCLIWaitDefault, "total invocation deadline (up to 1h)")
	fs.DurationVar(&o.interval, "poll-interval", api.AutomationLiveProgressPollInterval, "poll interval (1s to 1m)")
	var cases multiFlag
	fs.Var(&cases, "case", "selected saved case; repeat in execution order, at most 32")
	if err := fs.Parse(args); err != nil {
		return o, errors.New("could not parse suite flags; use automations live suite --help")
	}
	if fs.NArg() != 0 || !o.plan.Target.ConfirmRealActions || o.timeout <= 0 || o.timeout > api.AutomationLiveCLIWaitMax || o.interval < api.AutomationLiveCLIPollMin || o.interval > api.AutomationLiveCLIPollMax {
		return o, errors.New("confirm real actions; use a positive total timeout up to 1h, a poll interval from 1s to 1m, and no positional arguments")
	}
	if o.resume != "" {
		invalid := false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "resume", "confirm-real-actions", "timeout", "poll-interval":
			default:
				invalid = true
			}
		})
		if invalid {
			return o, errors.New("resume reads the original journal selection; use only --resume, --confirm-real-actions and optional wait settings")
		}
		o.path = o.resume
	} else {
		o.plan.Cases = append([]string(nil), cases...)
		if o.path == "" || !validAutomationLiveSuiteSelection(o.plan) {
			return o, errors.New("a new suite requires --journal, --app, --name, explicit target versions/deployment, a stable --idempotency-key and 1..32 unique --case values")
		}
	}
	var err error
	o.path, err = filepath.Abs(o.path)
	return o, err
}

func validAutomationLiveSuiteSelection(p automationLiveSuitePlan) bool {
	if !automationLiveName(p.App, api.AutomationNameMaxBytes) || !automationLiveName(p.Name, api.AutomationNameMaxBytes) || p.Target.ExpectedVersion <= 0 || p.Target.ExpectedPublishedVersion <= 0 || !automationLiveUUID(p.Target.ExpectedDeploymentID) || !p.Target.ConfirmRealActions || p.Target.CaseName != "" || strings.TrimSpace(p.Key) == "" || strings.TrimSpace(p.Key) != p.Key || validateDeployIdempotencyKey(p.Key) != nil || len(p.Cases) == 0 || len(p.Cases) > api.AutomationLiveSuiteMaxCases {
		return false
	}
	seen := map[string]bool{}
	for _, name := range p.Cases {
		if !automationLiveName(name, api.AutomationTestCaseNameMaxBytes) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func liveSuiteHash(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validLiveSuiteHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && value == strings.ToLower(value)
}

func liveSuiteCaseKey(p automationLiveSuitePlan, name string) string {
	target := p.Target
	id, _ := uuid.Parse(target.ExpectedDeploymentID)
	target.ExpectedDeploymentID, target.CaseName = id.String(), name
	return liveSuiteHash(struct {
		Domain, Endpoint, AppID, Name, Key string
		Target                             api.StartAutomationLiveVerificationRequest
	}{"gregale-live-suite-v1", p.EndpointHash, p.AppID, p.Name, p.Key, target})
}

func prepareAutomationLiveSuite(ctx context.Context, client *api.Client, o automationLiveSuiteOptions) (automationLiveSuiteJournal, error) {
	var j automationLiveSuiteJournal
	var err error
	if o.resume != "" {
		j, err = readAutomationLiveSuite(o.path)
		if err != nil {
			return j, err
		}
		if j.Plan.EndpointHash != liveSuiteHash(apiBase()) {
			return j, errors.New("suite API endpoint changed; use the original endpoint")
		}
		return j, nil
	}
	if _, err = os.Lstat(o.path); !errors.Is(err, os.ErrNotExist) {
		return j, errors.New("new journal must not exist; use --resume to continue an existing suite")
	}
	o.plan.EndpointHash, o.plan.CreatedAt = liveSuiteHash(apiBase()), time.Now().UTC()
	requestCtx, cancel := context.WithTimeout(ctx, api.AutomationLiveProgressRequestTimeout)
	app, err := client.GetApp(requestCtx, o.plan.App)
	cancel()
	if err != nil {
		return j, &api.APIError{Problem: api.Problem{Status: liveSuiteHTTPStatus(err), Code: "live_suite_scope_unavailable", Title: "Suite app unavailable", Detail: "Could not bind the suite app identity."}}
	}
	if !automationLiveUUID(app.ID) || app.Slug != o.plan.App {
		return j, errors.New("could not bind suite app identity")
	}
	o.plan.AppID = app.ID
	j = automationLiveSuiteJournal{SchemaVersion: 1, Plan: o.plan, Fingerprint: liveSuiteHash(o.plan)}
	for _, name := range o.plan.Cases {
		j.Cases = append(j.Cases, automationLiveSuiteCase{Name: name, Key: liveSuiteCaseKey(o.plan, name), Outcome: "not_started"})
	}
	// Write a complete frozen selection before any real-action request.
	err = saveAutomationLiveSuite(o.path, &j, true)
	return j, err
}
