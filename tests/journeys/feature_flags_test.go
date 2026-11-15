package journeys

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestFeatureFlagsJourney deploys a one-service project whose app evaluates
// flags with the Node runtime SDK, and drives Gregale Flags from the CLI
// (ADR-377, ADR-20261114150000, docs/flags.md):
//   - a customer rule serves true to the targeted customer only, identified
//     by the gateway from that customer's API key;
//   - an app.plan attribute rule matches on the attribute the app supplies;
//   - a kill serves the code's fallback with reason killed, carried by the
//     gateway's X-Faas-Flag-Kills header, and unkilling restores the rule;
//   - owner and expiry show in the lifecycle report.
//
// It skips while the target has Flags off (platform flag customer_flags,
// ADR-1700) and when sdk/node/dist/flags.js has not been built.
func TestFeatureFlagsJourney(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	project := tg.slug("flags")
	// The project does not exist yet, so the API answers not_found once Flags
	// are on for this account.
	if res := tg.run(t, time.Minute, "flag", "list", "--project", project); res.exit != 0 && strings.Contains(res.problem(), "Flags unavailable") {
		t.Skip("Flags are off for this account on this target (customer_flags)")
	}
	sdk, err := os.ReadFile(filepath.Join("..", "..", "sdk", "node", "dist", "flags.js"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("sdk/node/dist/flags.js is not built; run npm run build in sdk/node")
	} else if err != nil {
		t.Fatalf("read the Node Flags SDK: %v", err)
	}
	web := tg.app(t, "flweb")
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	base := tg.appURL(web)
	customers := map[string]flagsCustomer{}
	s := newStory(t)

	s.step("deploy_project", func(t *testing.T) {
		dir := flagsWebSource(t, sdk)
		compose := "services:\n  " + web + ":\n    build: ./web\n    ports: [\"8080:8080\"]\n" +
			"    environment:\n      GREGALE_API_URL: " + tg.api + "\n"
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
			t.Fatalf("write compose.yaml: %v", err)
		}
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--project",
			"--project-slug", project, "--path", dir, "--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("project deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		// Customers call the app with their own API keys; the gateway turns
		// a key into a verified customer identity.
		tg.ok(t, time.Minute, "app", web, "--no-require-authn", "--consumer-auth-mode", "required", "--platform-tenant-required")
		eventually(t, "the app serves", 3*time.Minute, 3*time.Second, func() (bool, string) {
			r := get(base + "/healthz")
			return r.status == 200, r.String()
		})
	})

	s.step("register_customers", func(t *testing.T) {
		for _, name := range []string{"pilot", "other"} {
			customers[name] = tg.flagsCustomer(t, web, name)
		}
	})

	s.step("customer_rule_targets_one_customer", func(t *testing.T) {
		tg.ok(t, time.Minute, "flag", "create", "journey-export", "--project", project, "--owner", "journeys")
		tg.ok(t, time.Minute, "flag", "rule", "create", "journey-export", "--id", "pilot",
			"--customer", customers["pilot"].tenantID, "--project", project)
		// The SDK refreshes its configuration every 15 s.
		start := time.Now()
		eventually(t, "the pilot customer gets the flag", 90*time.Second, 3*time.Second, func() (bool, string) {
			d := tg.flagCheck(customers["pilot"], base, "journey-export", "")
			return d.Value && d.Reason == "rule_match" && d.RuleID == "pilot", d.String()
		})
		t.Logf("metric flags_publish_to_decision_s=%.1f", time.Since(start).Seconds())
		if d := tg.flagCheck(customers["other"], base, "journey-export", ""); d.Value || d.Source == "fallback" {
			t.Fatalf("other customer = %s; want the configured default false", d)
		}
	})

	s.step("attribute_rule_matches_app_plan", func(t *testing.T) {
		tg.ok(t, time.Minute, "flag", "create", "journey-plan", "--project", project)
		tg.ok(t, time.Minute, "flag", "rule", "create", "journey-plan", "--id", "pro", "--attr", "plan=pro", "--project", project)
		eventually(t, "plan=pro gets the flag", 90*time.Second, 3*time.Second, func() (bool, string) {
			d := tg.flagCheck(customers["other"], base, "journey-plan", "pro")
			return d.Value && d.RuleID == "pro", d.String()
		})
		if d := tg.flagCheck(customers["other"], base, "journey-plan", "free"); d.Value {
			t.Fatalf("plan=free = %s; want false", d)
		}
		res := tg.ok(t, time.Minute, "flag", "inspect", "--project", project, "--key", "journey-plan",
			"--customer", customers["other"].tenantID, "--attr", "plan=pro")
		if doc := object(t, res.stdout); field(doc, "value") != true {
			t.Fatalf("inspect with plan=pro = %s; want value true", compact(doc))
		}
	})

	s.step("kill_serves_fallback_from_the_gateway", func(t *testing.T) {
		tg.ok(t, time.Minute, "flag", "kill", "set", "journey-export", "--project", project)
		start := time.Now()
		var killed flagDecision
		eventually(t, "the kill reaches the app", 30*time.Second, time.Second, func() (bool, string) {
			killed = tg.flagCheck(customers["pilot"], base, "journey-export", "")
			return !killed.Value && killed.Reason == "killed", killed.String()
		})
		t.Logf("metric flags_kill_to_decision_s=%.1f", time.Since(start).Seconds())
		if !strings.Contains(","+killed.Kills+",", ",journey-export,") {
			t.Errorf("kill decided without the gateway header (kills=%q): the SDK picked it up on a refresh", killed.Kills)
		}
		tg.ok(t, time.Minute, "flag", "kill", "unset", "journey-export", "--project", project)
		eventually(t, "the unkill restores the rule", 90*time.Second, 3*time.Second, func() (bool, string) {
			d := tg.flagCheck(customers["pilot"], base, "journey-export", "")
			return d.Value && d.Reason == "rule_match", d.String()
		})
	})

	s.check("lifecycle_reports_owner_and_expiry", func(t *testing.T) {
		expires := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
		tg.ok(t, time.Minute, "flag", "set", "journey-export", "--expires", expires, "--project", project)
		res := tg.ok(t, time.Minute, "flag", "stale", "list", "--all", "--project", project)
		doc := object(t, res.stdout)
		seen := map[string]any{}
		for _, f := range asSlice(field(doc, "flags")) {
			seen[str(f, "key")] = f
		}
		export, plan := seen["journey-export"], seen["journey-plan"]
		if export == nil || plan == nil {
			t.Fatalf("lifecycle report = %s; want both journey flags", compact(doc))
		}
		if str(export, "owner") != "journeys" || str(export, "expires_at") != expires || !hasString(field(export, "attention"), "expires_soon") {
			t.Errorf("journey-export lifecycle = %s; want owner journeys, expires_at %s, attention expires_soon", compact(export), expires)
		}
		if !hasString(field(plan, "attention"), "no_owner") {
			t.Errorf("journey-plan lifecycle = %s; want attention no_owner", compact(plan))
		}
	})
}

// flagsCustomer is one verified customer of the journey's app: a platform
// tenant linked to an API consumer whose key the journey calls the app with.
type flagsCustomer struct {
	tenantID string
	key      string
}

func (tg *target) flagsCustomer(t *testing.T, app, name string) flagsCustomer {
	t.Helper()
	ref := tg.slug("flags-" + name)
	tenant := object(t, tg.ok(t, time.Minute, "api", "tenant", "create", "--external-ref", ref, "--name", "Flags journey "+name).stdout)
	consumer := object(t, tg.ok(t, time.Minute, "api", "consumer", "create", app, "--external-ref", ref, "--name", "Flags journey "+name).stdout)
	tenantID, consumerID := str(tenant, "id"), str(consumer, "id")
	if tenantID == "" || consumerID == "" {
		t.Fatalf("customer %s: tenant=%s consumer=%s; want ids", name, compact(tenant), compact(consumer))
	}
	tg.ok(t, time.Minute, "api", "tenant", "consumer", "link", "--id", tenantID, "--consumer-id", consumerID)
	key := str(object(t, tg.ok(t, time.Minute, "api", "consumer", "key", "create", app, consumerID, "--name", "flags-journey", "--scopes", "read").stdout), "key")
	if key == "" {
		t.Fatalf("customer %s: consumer key create returned no key", name)
	}
	tg.addSensitive(key)
	return flagsCustomer{tenantID: tenantID, key: key}
}

// flagDecision is the SDK decision the fixture app returns.
type flagDecision struct {
	Value         bool   `json:"value"`
	Reason        string `json:"reason"`
	RuleID        string `json:"rule_id"`
	Source        string `json:"source"`
	ConfigVersion int64  `json:"config_version"`
	Kills         string `json:"kills"`
	Error         string `json:"error"`
	status        int
}

func (d flagDecision) String() string {
	b, _ := json.Marshal(d)
	return "status " + strconv.Itoa(d.status) + " " + string(b)
}

func (tg *target) flagCheck(c flagsCustomer, base, flag, plan string) flagDecision {
	u := base + "/check?flag=" + flag
	if plan != "" {
		u += "&plan=" + plan
	}
	r := fetch("GET", u, map[string]string{"Authorization": "Bearer " + c.key})
	var d flagDecision
	_ = json.Unmarshal(r.body, &d)
	d.status = r.status
	return d
}

// flagsWebSource copies the flags-web fixture into a project directory and
// vendors the built Node Flags SDK beside it.
func flagsWebSource(t *testing.T, sdk []byte) string {
	t.Helper()
	dir := t.TempDir()
	web := filepath.Join(dir, "web")
	if err := os.MkdirAll(filepath.Join(web, "vendor"), 0o755); err != nil {
		t.Fatalf("create flags source: %v", err)
	}
	for _, name := range []string{"package.json", "server.js"} {
		b, err := os.ReadFile(filepath.Join("testdata", "flags-web", name))
		if err != nil {
			t.Fatalf("read flags fixture: %v", err)
		}
		if err := os.WriteFile(filepath.Join(web, name), b, 0o644); err != nil {
			t.Fatalf("write flags fixture: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(web, "vendor", "flags.js"), sdk, 0o644); err != nil {
		t.Fatalf("vendor the Flags SDK: %v", err)
	}
	return dir
}

func hasString(v any, want string) bool {
	for _, s := range asSlice(v) {
		if s == want {
			return true
		}
	}
	return false
}
