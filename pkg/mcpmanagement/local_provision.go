package mcpmanagement

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Provisioning tools attach what an app needs next to its code — a custom
// domain, a bucket, a managed PostgreSQL database, an HTTP cron — through the
// same CLI commands a person runs (ADR-20261026174727). Like local deploys,
// they exist only on local connections, plan before they apply, and keep
// plans in connection memory.
const provisionTimeout = 12 * time.Minute

var (
	domainName     = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	bucketName     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	regionName     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	timezoneName   = regexp.MustCompile(`^[A-Za-z0-9_+/-]{1,64}$`)
	bucketAccess   = map[string]bool{"read": true, "write": true, "read_write": true}
	postgresAccess = map[string]bool{"read_write": true, "read_only": true, "migration": true}
)

type provisionPlan struct {
	Kind    string   `json:"kind"`
	Command []string `json:"command"`
	// AlreadyAttached: the domain is on this app; apply only reports it.
	AlreadyAttached bool `json:"already_attached,omitempty"`
	// DuplicateOf names an existing cron with the same schedule and path.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	domain      string
	cron        cronSpec
	existing    map[string]bool
}

type cronSpec struct{ Schedule, Path, Timezone string }

type planDomainArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Domain string `json:"domain" jsonschema:"Domain to serve the app on, for example api.example.com"`
	Reason string `json:"reason" jsonschema:"Why; at most 280 characters"`
}

type planBucketArgs struct {
	App        string `json:"app" jsonschema:"App slug"`
	Name       string `json:"name" jsonschema:"Bucket name, lowercase letters, digits and hyphens"`
	Permission string `json:"permission,omitempty" jsonschema:"read, write or read_write (default)"`
	Reason     string `json:"reason" jsonschema:"Why; at most 280 characters"`
}

type planPostgresArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Region string `json:"region" jsonschema:"Region for a new database"`
	Access string `json:"access,omitempty" jsonschema:"read_write (default), read_only or migration"`
	Reason string `json:"reason" jsonschema:"Why; at most 280 characters"`
}

type planCronArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	Schedule string `json:"schedule" jsonschema:"Cron expression, for example */15 * * * *"`
	Path     string `json:"path" jsonschema:"Path the cron POSTs to, starting with /"`
	Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone; default UTC"`
	Reason   string `json:"reason" jsonschema:"Why; at most 280 characters"`
}

type checkDomainArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Domain string `json:"domain" jsonschema:"Domain attached with gregale_apply_provision"`
}

func registerLocalProvisioning(s *mcp.Server, m *management) {
	// A hosted grant provisions through the bridge's runner as itself, only
	// with write consent and each scope its consent added
	// (ADR-20261026195952). Local app consent stays out.
	remote := m.opts.Environments != nil
	if m.opts.LocalCLI == nil || !remote && m.opts.AllowedApps != nil || remote && !m.opts.AllowWrites {
		return
	}
	consented := func(scope string) bool { return !remote || slices.Contains(m.opts.Capabilities, scope) }
	register(s, m, "gregale_plan_domain", "Review attaching a custom domain to an app. Apply returns the DNS records the user must create at their DNS provider; then check with gregale_check_domain.", m.planDomain)
	if consented(api.ScopeStorageManage) {
		register(s, m, "gregale_plan_bucket", "Review creating a private object storage bucket for an app and binding it; the app receives GREGALE_S3_<NAME>_* variables on its next start.", m.planBucket)
	}
	if consented(api.ScopeManagedPostgresManage) {
		register(s, m, "gregale_plan_postgres", "Review creating a managed PostgreSQL database for an app and binding it as DATABASE_URL. Apply can take up to ten minutes.", m.planPostgres)
	}
	register(s, m, "gregale_plan_cron", "Review adding a cron that POSTs to an app path on a schedule.", m.planCron)
	const checkDomain = "Check a custom domain's DNS verification and TLS certificate; returns the DNS records still needed."
	if m.opts.AllowWrites {
		// `domains verify` asks Gregale to re-check DNS now (a POST), so the
		// check is a write; a read-only connection reads the last result.
		registerTool(s, m, "gregale_check_domain", checkDomain, false, m.verifyDomain)
		registerTool(s, m, "gregale_apply_provision", "Apply a reviewed domain, bucket, database or cron plan through the gregale CLI. Retry with the same plan ID after ambiguous errors.", false, m.applyProvision)
	} else {
		register(s, m, "gregale_check_domain", checkDomain, m.showDomain)
	}
}

func (m *management) planDomain(ctx context.Context, args planDomainArgs) (any, error) {
	domain := strings.ToLower(strings.TrimSuffix(args.Domain, "."))
	if len(domain) > 253 || !domainName.MatchString(domain) {
		return nil, invalid("invalid_argument", "domain must be a hostname such as api.example.com")
	}
	p, err := m.provisionBase(ctx, args.App, args.Reason, "domain")
	if err != nil {
		return nil, err
	}
	p.Provision.domain, p.Provision.Command = domain, []string{"domains", "add", domain, "--app", args.App}
	attached, err := cliNDJSON(ctx, m, "domains", "list", "--app", args.App)
	if err != nil {
		return nil, err
	}
	for _, d := range attached {
		if d["domain"] == domain {
			p.Provision.AlreadyAttached = true
		}
	}
	p.TrafficEffect = "Attach " + domain + " to " + args.App + ". It serves once the user's DNS records verify and the certificate is issued."
	return m.savePlan(p)
}

func (m *management) planBucket(ctx context.Context, args planBucketArgs) (any, error) {
	permission := args.Permission
	if permission == "" {
		permission = "read_write"
	}
	if !bucketName.MatchString(args.Name) || !bucketAccess[permission] {
		return nil, invalid("invalid_argument", "name must match ^[a-z][a-z0-9-]{0,62}$ and permission must be read, write or read_write")
	}
	p, err := m.provisionBase(ctx, args.App, args.Reason, "bucket")
	if err != nil {
		return nil, err
	}
	p.Provision.Command = []string{"add", "bucket", "--app", args.App, "--env", api.DefaultEnvScope, "--permission", permission, args.Name}
	p.TrafficEffect = "Create or reuse the private bucket " + args.Name + " and bind it to " + args.App + "; its credentials arrive as sealed variables on the next start."
	return m.savePlan(p)
}

func (m *management) planPostgres(ctx context.Context, args planPostgresArgs) (any, error) {
	access := args.Access
	if access == "" {
		access = "read_write"
	}
	if !regionName.MatchString(args.Region) || !postgresAccess[access] {
		return nil, invalid("invalid_argument", "region must be a region name and access must be read_write, read_only or migration")
	}
	p, err := m.provisionBase(ctx, args.App, args.Reason, "postgres")
	if err != nil {
		return nil, err
	}
	// Flags precede everything: a bare word after `add postgres` is read as the app.
	p.Provision.Command = []string{"add", "postgres", "--app", args.App, "--env", api.DefaultEnvScope, "--region", args.Region, "--access", access}
	p.TrafficEffect = "Create or reuse " + args.App + "-postgres in " + args.Region + ", bind it as DATABASE_URL and open egress port 5432. Database usage is billed."
	return m.savePlan(p)
}

func (m *management) planCron(ctx context.Context, args planCronArgs) (any, error) {
	if args.Schedule == "" || len(args.Schedule) > 128 || strings.ContainsAny(args.Schedule, "\r\n") || !strings.HasPrefix(args.Path, "/") || len(args.Path) > 512 || strings.ContainsAny(args.Path, " \r\n") || (args.Timezone != "" && !timezoneName.MatchString(args.Timezone)) {
		return nil, invalid("invalid_argument", "schedule must be a cron expression, path must start with /, and timezone must be an IANA name")
	}
	p, err := m.provisionBase(ctx, args.App, args.Reason, "cron")
	if err != nil {
		return nil, err
	}
	spec := cronSpec{Schedule: args.Schedule, Path: args.Path, Timezone: args.Timezone}
	p.Provision.cron, p.Provision.existing = spec, map[string]bool{}
	p.Provision.Command = []string{"crons", "add", "--app", args.App, "--schedule", spec.Schedule, "--path", spec.Path}
	if spec.Timezone != "" {
		p.Provision.Command = append(p.Provision.Command, "--timezone", spec.Timezone)
	}
	crons, err := cliNDJSON(ctx, m, "crons", "list", "--app", args.App)
	if err != nil {
		return nil, err
	}
	for _, c := range crons {
		id, _ := c["id"].(string)
		p.Provision.existing[id] = true
		if cronMatches(c, spec) {
			p.Provision.DuplicateOf = id
		}
	}
	p.TrafficEffect = "POST " + spec.Path + " on " + args.App + " on schedule " + spec.Schedule + "."
	return m.savePlan(p)
}

func cronMatches(c map[string]any, spec cronSpec) bool {
	tz, _ := c["timezone"].(string)
	return c["schedule"] == spec.Schedule && c["path"] == spec.Path && (tz == spec.Timezone || spec.Timezone == "" && (tz == "" || tz == "UTC"))
}

// provisionBase pins the account and app every provisioning plan acts on.
func (m *management) provisionBase(ctx context.Context, app, reason, kind string) (*releasePlan, error) {
	if err := validateReason(app, reason); err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	target, err := m.client.GetApp(ctx, app)
	if err != nil {
		return nil, err
	}
	return &releasePlan{Action: "provision_" + kind, AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: target.ID, App: app, Scope: api.DefaultEnvScope, Reason: reason, ExpiresAt: time.Now().Add(releasePlanTTL), Provision: &provisionPlan{Kind: kind}}, nil
}

func (m *management) applyProvision(ctx context.Context, args applyArgs) (any, error) {
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "restart the connection with --allow-writes to enable reviewed release actions")
	}
	m.mu.Lock()
	p := m.plans[args.PlanID]
	var receipt any
	if p != nil {
		receipt = p.localReceipt
	}
	m.mu.Unlock()
	if p == nil || p.Provision == nil {
		return nil, invalid("invalid_plan", "plan ID is unknown or belongs to a different action or connection")
	}
	if receipt != nil {
		return receipt, nil
	}
	if time.Now().After(p.ExpiresAt) {
		return nil, invalid("plan_expired", "plan expired; plan again")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	app, err := m.client.GetApp(ctx, p.App)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID || app.ID != p.AppID {
		return nil, invalid("plan_stale", "account or app identity changed; plan again")
	}
	m.mu.Lock()
	if p.localInFlight {
		m.mu.Unlock()
		return nil, invalid("in_progress", "this plan is already being applied; retry the same plan ID when that call returns")
	}
	retry := p.attempted
	p.attempted, p.localInFlight = true, true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		p.localInFlight = false
		m.mu.Unlock()
	}()
	var result map[string]any
	switch p.Provision.Kind {
	case "domain":
		result, err = m.applyDomain(ctx, p)
	case "cron":
		result, err = m.applyCron(ctx, p, retry)
	default:
		result, err = cliObject(ctx, m, p.Provision.Command...)
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"plan_id": p.ID, "app": p.App, "kind": p.Provision.Kind, "result": result}
	switch p.Provision.Kind {
	case "bucket", "postgres":
		out["next"] = "Restart or redeploy the app so running instances receive the new variables."
	case "domain":
		out["next"] = "Ask the user to create the dns_records at their DNS provider, then call gregale_check_domain."
	}
	m.mu.Lock()
	p.localReceipt = out
	m.mu.Unlock()
	return out, nil
}

func (m *management) applyDomain(ctx context.Context, p *releasePlan) (map[string]any, error) {
	if !p.Provision.AlreadyAttached {
		if _, err := m.runCLI(ctx, p.Provision.Command, nil); err != nil {
			// A retry finds the domain this plan already attached.
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Problem.Status != 409 {
				return nil, err
			}
		}
	}
	return m.domainOnApp(ctx, p.AppID, p.Provision.domain, "show")
}

// applyCron never creates a second cron after an ambiguous attempt: a new cron
// with the planned schedule and path is adopted instead.
func (m *management) applyCron(ctx context.Context, p *releasePlan, retry bool) (map[string]any, error) {
	if retry {
		crons, err := cliNDJSON(ctx, m, "crons", "list", "--app", p.App)
		if err != nil {
			return nil, err
		}
		for _, c := range crons {
			if id, _ := c["id"].(string); !p.Provision.existing[id] && cronMatches(c, p.Provision.cron) {
				return c, nil
			}
		}
	}
	return cliObject(ctx, m, p.Provision.Command...)
}

func (m *management) verifyDomain(ctx context.Context, args checkDomainArgs) (any, error) {
	return m.checkDomain(ctx, args, "verify")
}

func (m *management) showDomain(ctx context.Context, args checkDomainArgs) (any, error) {
	return m.checkDomain(ctx, args, "show")
}

func (m *management) checkDomain(ctx context.Context, args checkDomainArgs, verb string) (any, error) {
	domain := strings.ToLower(strings.TrimSuffix(args.Domain, "."))
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	if !domainName.MatchString(domain) {
		return nil, invalid("invalid_argument", "domain must be a hostname such as api.example.com")
	}
	app, err := m.client.GetApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	return m.domainOnApp(ctx, app.ID, domain, verb)
}

// domainOnApp runs `domains show` or `domains verify` and keeps the fields an
// agent needs. verify exits 1 while DNS is pending but still prints the domain.
func (m *management) domainOnApp(ctx context.Context, appID, domain, verb string) (map[string]any, error) {
	result, err := m.opts.LocalCLI(ctx, []string{"domains", verb, domain}, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, invalid("cli_unavailable", "could not run the local gregale CLI: "+err.Error())
	}
	var d map[string]any
	if json.Unmarshal(bytes.TrimSpace(result.Stdout), &d) != nil || d["domain"] != domain {
		if result.ExitCode != 0 {
			return nil, cliProblem(result)
		}
		return nil, invalid("invalid_response", "the CLI returned no domain")
	}
	if d["app_id"] != appID {
		return nil, invalid("domain_elsewhere", "this domain is attached to another app")
	}
	out := map[string]any{"domain": domain, "pending": result.ExitCode != 0}
	for _, key := range []string{"verified", "cert_status", "cert_last_error", "dns_records"} {
		if value, ok := d[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func cliObject(ctx context.Context, m *management, args ...string) (map[string]any, error) {
	stdout, err := m.runCLI(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return nil, invalid("invalid_response", fmt.Sprintf("the CLI returned no JSON receipt for %s %s", args[0], args[1]))
	}
	return out, nil
}

func cliNDJSON(ctx context.Context, m *management, args ...string) ([]map[string]any, error) {
	stdout, err := m.runCLI(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	lines := bufio.NewScanner(bytes.NewReader(stdout))
	lines.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for lines.Scan() {
		line := bytes.TrimSpace(lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal(line, &item); err != nil || !utf8.Valid(line) {
			return nil, invalid("invalid_response", fmt.Sprintf("the CLI returned an unreadable %s list", args[0]))
		}
		out = append(out, item)
	}
	return out, lines.Err()
}
