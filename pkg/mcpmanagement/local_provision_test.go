// adr: 20261026174727
package mcpmanagement

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// provisionCLI fakes the CLI commands the provisioning tools run.
type provisionCLI struct {
	mu            sync.Mutex
	calls         [][]string
	domains       map[string]string // domain → app ID
	verified      bool
	crons         []map[string]any
	failAfterCron bool
}

func newProvisionCLI() *provisionCLI {
	return &provisionCLI{domains: map[string]string{}}
}

func (c *provisionCLI) cli(_ context.Context, args []string, _ []byte) (LocalCLIResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, slices.Clone(args))
	ok := func(v any) (LocalCLIResult, error) {
		data, _ := json.Marshal(v)
		return LocalCLIResult{Stdout: data}, nil
	}
	ndjson := func(items []map[string]any) (LocalCLIResult, error) {
		var out []byte
		for _, item := range items {
			line, _ := json.Marshal(item)
			out = append(append(out, line...), '\n')
		}
		return LocalCLIResult{Stdout: out}, nil
	}
	domainJSON := func(d string) map[string]any {
		return map[string]any{"domain": d, "app_id": c.domains[d], "verified": c.verified, "cert_status": map[bool]string{true: "issued", false: "pending"}[c.verified],
			"challenge_token": "secret-ish", "dns_records": []map[string]any{{"type": "CNAME", "name": d, "value": "alpha.gregale.dev", "purpose": "routing"}}}
	}
	problem := func(status int, code string) (LocalCLIResult, error) {
		data, _ := json.Marshal(api.Problem{Status: status, Code: code, Title: code})
		return LocalCLIResult{Stderr: data, ExitCode: 1}, nil
	}
	switch strings.Join(args[:2], " ") {
	case "domains list":
		var items []map[string]any
		for d, app := range c.domains {
			if app == "app-one" {
				items = append(items, domainJSON(d))
			}
		}
		return ndjson(items)
	case "domains add":
		if _, taken := c.domains[args[2]]; taken {
			return problem(409, "domain_taken")
		}
		c.domains[args[2]] = "app-one"
		return LocalCLIResult{Stdout: []byte("Added " + args[2] + "\n")}, nil
	case "domains show", "domains verify":
		if _, known := c.domains[args[2]]; !known {
			return problem(404, "not_found")
		}
		data, _ := json.Marshal(domainJSON(args[2]))
		exit := 0
		if args[1] == "verify" && !c.verified {
			exit = 1 // pending DNS still prints the domain
		}
		return LocalCLIResult{Stdout: data, ExitCode: exit}, nil
	case "add bucket":
		return ok(map[string]any{"app_id": "app-one", "bucket": map[string]any{"name": args[len(args)-1]}, "binding": map[string]any{"prefix": "GREGALE_S3_ASSETS"}, "bucket_created": true})
	case "add postgres":
		return ok(map[string]any{"app_id": "app-one", "database": map[string]any{"name": "alpha-postgres"}, "binding": map[string]any{"environment_key": "DATABASE_URL"}, "database_created": true})
	case "crons list":
		return ndjson(c.crons)
	case "crons add":
		cron := map[string]any{"id": "cron-" + string(rune('a'+len(c.crons))), "schedule": args[slices.Index(args, "--schedule")+1], "path": args[slices.Index(args, "--path")+1]}
		c.crons = append(c.crons, cron)
		if c.failAfterCron {
			c.failAfterCron = false
			return problem(0, "transport_error")
		}
		return ok(cron)
	}
	return problem(400, "unexpected_command")
}

func (c *provisionCLI) count(verb string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, call := range c.calls {
		if strings.Join(call[:2], " ") == verb {
			n++
		}
	}
	return n
}

func connectProvision(t *testing.T, cli *provisionCLI, writes bool, mutate ...func(*Options)) (*localFixture, func(string, map[string]any, bool) map[string]json.RawMessage) {
	t.Helper()
	f := newLocalFixture(t, true, "release-base")
	cs, ctx := f.connect(t, writes, append([]func(*Options){func(o *Options) { o.LocalCLI = cli.cli }}, mutate...)...)
	return f, func(name string, args map[string]any, wantError bool) map[string]json.RawMessage {
		return callTool(t, ctx, cs, name, args, wantError)
	}
}

func TestProvisionToolsOnlyOnLocalConnections(t *testing.T) {
	all := []string{"gregale_plan_domain", "gregale_plan_bucket", "gregale_plan_postgres", "gregale_plan_cron", "gregale_check_domain", "gregale_apply_provision"}
	for _, tc := range []struct {
		name   string
		writes bool
		mutate func(*Options)
		want   []string
	}{
		{"local writes", true, func(*Options) {}, all},
		{"local read-only", false, func(*Options) {}, all[:5]},
		{"app consent", true, func(o *Options) { o.AllowedApps = map[string]string{"alpha": "app-one"} }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalFixture(t, true, "")
			cs, ctx := f.connect(t, tc.writes, func(o *Options) { o.LocalCLI = newProvisionCLI().cli }, tc.mutate)
			tools := listedTools(t, ctx, cs)
			for _, name := range all {
				if tools[name] != slices.Contains(tc.want, name) {
					t.Errorf("%s listed=%v", name, tools[name])
				}
			}
		})
	}
}

func TestProvisionDomainAttachesAndChecksDNS(t *testing.T) {
	cli := newProvisionCLI()
	_, call := connectProvision(t, cli, true)
	plan := call("gregale_plan_domain", map[string]any{"app": "alpha", "domain": "API.Example.com.", "reason": "custom domain"}, false)
	if !strings.Contains(string(plan["provision"]), `"domains","add","api.example.com","--app","alpha"`) {
		t.Fatalf("plan = %v", plan)
	}
	applied := call("gregale_apply_provision", map[string]any{"plan_id": planID(t, plan)}, false)
	if !strings.Contains(string(applied["result"]), `"CNAME"`) || strings.Contains(string(applied["result"]), "challenge_token") || !strings.Contains(string(applied["next"]), "gregale_check_domain") {
		t.Fatalf("apply = %v", applied)
	}
	pending := call("gregale_check_domain", map[string]any{"app": "alpha", "domain": "api.example.com"}, false)
	if string(pending["pending"]) != "true" || string(pending["verified"]) != "false" {
		t.Fatalf("pending check = %v", pending)
	}
	cli.mu.Lock()
	cli.verified = true
	cli.domains["shop.example.com"] = "app-other"
	cli.mu.Unlock()
	if done := call("gregale_check_domain", map[string]any{"app": "alpha", "domain": "api.example.com"}, false); string(done["pending"]) != "false" || string(done["verified"]) != "true" {
		t.Fatalf("verified check = %v", done)
	}
	if elsewhere := call("gregale_check_domain", map[string]any{"app": "alpha", "domain": "shop.example.com"}, true); toolCode(t, elsewhere) != "domain_elsewhere" {
		t.Fatalf("foreign domain = %v", elsewhere)
	}
}

func TestProvisionDomainRetryAdoptsTheAttachedDomain(t *testing.T) {
	cli := newProvisionCLI()
	_, call := connectProvision(t, cli, true)
	id := planID(t, call("gregale_plan_domain", map[string]any{"app": "alpha", "domain": "api.example.com", "reason": "custom domain"}, false))
	cli.mu.Lock()
	cli.domains["api.example.com"] = "app-one" // an earlier ambiguous attempt landed
	cli.mu.Unlock()
	if applied := call("gregale_apply_provision", map[string]any{"plan_id": id}, false); !strings.Contains(string(applied["result"]), "api.example.com") {
		t.Fatalf("apply = %v", applied)
	}
}

func TestProvisionBucketAndPostgresRunTheCLI(t *testing.T) {
	cli := newProvisionCLI()
	_, call := connectProvision(t, cli, true)
	bucket := planID(t, call("gregale_plan_bucket", map[string]any{"app": "alpha", "name": "assets", "reason": "uploads"}, false))
	first := call("gregale_apply_provision", map[string]any{"plan_id": bucket}, false)
	again := call("gregale_apply_provision", map[string]any{"plan_id": bucket}, false)
	if string(first["result"]) != string(again["result"]) || cli.count("add bucket") != 1 {
		t.Fatalf("bucket apply not cached: %v / %v", first, again)
	}
	pg := planID(t, call("gregale_plan_postgres", map[string]any{"app": "alpha", "region": "eu-central", "reason": "orders"}, false))
	call("gregale_apply_provision", map[string]any{"plan_id": pg}, false)
	cli.mu.Lock()
	defer cli.mu.Unlock()
	var bucketCall, pgCall []string
	for _, c := range cli.calls {
		switch strings.Join(c[:2], " ") {
		case "add bucket":
			bucketCall = c
		case "add postgres":
			pgCall = c
		}
	}
	if !slices.Equal(bucketCall, []string{"add", "bucket", "--app", "alpha", "--env", "default", "--permission", "read_write", "assets"}) {
		t.Errorf("bucket command = %v", bucketCall)
	}
	if !slices.Equal(pgCall, []string{"add", "postgres", "--app", "alpha", "--env", "default", "--region", "eu-central", "--access", "read_write"}) {
		t.Errorf("postgres command = %v", pgCall)
	}
}

func TestProvisionCronRetryAdoptsInsteadOfDuplicating(t *testing.T) {
	cli := newProvisionCLI()
	cli.crons = []map[string]any{{"id": "cron-old", "schedule": "0 * * * *", "path": "/hourly"}}
	cli.failAfterCron = true
	_, call := connectProvision(t, cli, true)
	if dup := call("gregale_plan_cron", map[string]any{"app": "alpha", "schedule": "0 * * * *", "path": "/hourly", "reason": "again"}, false); !strings.Contains(string(dup["provision"]), `"duplicate_of":"cron-old"`) {
		t.Fatalf("duplicate not flagged: %v", dup)
	}
	id := planID(t, call("gregale_plan_cron", map[string]any{"app": "alpha", "schedule": "*/15 * * * *", "path": "/sync", "reason": "sync"}, false))
	if failed := call("gregale_apply_provision", map[string]any{"plan_id": id}, true); toolCode(t, failed) != "transport_error" {
		t.Fatalf("ambiguous apply = %v", failed)
	}
	adopted := call("gregale_apply_provision", map[string]any{"plan_id": id}, false)
	if cli.count("crons add") != 1 || len(cli.crons) != 2 || !strings.Contains(string(adopted["result"]), `"/sync"`) {
		t.Fatalf("retry created a duplicate or lost the cron: adds=%d crons=%v result=%v", cli.count("crons add"), cli.crons, adopted)
	}
}

func TestProvisionRejectsBadInput(t *testing.T) {
	_, call := connectProvision(t, newProvisionCLI(), true)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"gregale_plan_domain", map[string]any{"app": "alpha", "domain": "http://api.example.com", "reason": "x"}},
		{"gregale_plan_domain", map[string]any{"app": "alpha", "domain": "localhost", "reason": "x"}},
		{"gregale_plan_bucket", map[string]any{"app": "alpha", "name": "Assets", "reason": "x"}},
		{"gregale_plan_bucket", map[string]any{"app": "alpha", "name": "assets", "permission": "admin", "reason": "x"}},
		{"gregale_plan_postgres", map[string]any{"app": "alpha", "region": "", "reason": "x"}},
		{"gregale_plan_postgres", map[string]any{"app": "alpha", "region": "eu-central", "access": "data_api", "reason": "x"}},
		{"gregale_plan_cron", map[string]any{"app": "alpha", "schedule": "* * * * *", "path": "sync", "reason": "x"}},
		{"gregale_plan_cron", map[string]any{"app": "alpha", "schedule": "* * * * *", "path": "/sync", "timezone": "Europe/Paris; rm", "reason": "x"}},
		{"gregale_plan_cron", map[string]any{"app": "alpha", "schedule": "", "path": "/sync", "reason": "x"}},
	} {
		if out := call(tc.tool, tc.args, true); toolCode(t, out) != "invalid_argument" {
			t.Errorf("%s %v accepted: %v", tc.tool, tc.args, out)
		}
	}
}
