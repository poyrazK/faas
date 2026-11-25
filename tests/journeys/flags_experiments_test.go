package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// consumerKeyRe matches a consumer key (ck_<prefix>_<secret>) in CLI output.
var consumerKeyRe = regexp.MustCompile(`ck_[0-9a-f]{8}_[0-9a-f]{16,}`)

// flagsCustomers is how many verified customers send checkout traffic. With
// a 50/50 variant flag, eight customers leave both arms empty with
// probability 1/128.
const flagsCustomers = 8

// flagsCustomer is one verified customer: a platform tenant with a consumer
// of the journey's app and that consumer's key.
type flagsCustomer struct {
	tenant, consumer, key string
	variant               string
	converted             bool
}

// flagsJourney is the state TestFlagsAndExperiments' steps share.
type flagsJourney struct {
	tg *target
	// runtime is the probe's language: node, python or go (flagsProbes).
	runtime   string
	count     int // verified customers to register
	project   string
	app       string
	base      string
	customers []*flagsCustomer
}

// TestFlagsAndExperiments is a customer's Flags journey (ADR-20261115163113,
// ADR-20261116190208, ADR-20261119173702) through the CLI, a project
// function using the Node flags SDK and verified customers calling it with
// consumer keys:
//
//   - the SDK serves a variant flag the same way `flag inspect` explains it;
//   - an experiment counts every customer's exposure and conversion;
//   - a layer and the holdout can be managed, and the holdout is refused
//     while an experiment runs;
//   - promoting a variant serves it to every customer, and a kill serves
//     the code's fallback;
//   - with product analytics on, the tracked events are queryable.
//
// It skips when Flags are not released to the account.
func TestFlagsAndExperiments(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipFlagsUnreleased(t, tg)
	j := &flagsJourney{tg: tg, runtime: "node", count: flagsCustomers, project: tg.slug("flags")}
	j.app = j.project + "-web"
	tg.ownApp(t, j.app)
	t.Cleanup(func() {
		// Deleting the project detaches its app; ownApp's cleanup deletes it.
		if res := tg.run(t, 2*time.Minute, "project", "delete", j.project, "--yes"); res.exit != 0 {
			t.Logf("warning: cleanup could not delete project %s (%s)", j.project, res.problem())
		}
	})
	// The experiment outlives the step that starts it, so the test, not the
	// step, stops it; cleanups run last-in first-out, before the project goes.
	t.Cleanup(func() {
		tg.run(t, time.Minute, "flag", "experiment", "stop", "checkout-exp", "--project", j.project)
		tg.run(t, time.Minute, "flag", "experiment", "delete", "checkout-exp", "--project", j.project)
	})
	s := newStory(t)
	if !s.step("deploy_project", j.deploy) {
		return
	}
	if !s.step("verified_customers", j.makeCustomers) {
		return
	}
	if !s.step("create_variant_flag", j.createFlag) {
		return
	}
	s.check("sdk_matches_inspector", j.sdkMatchesInspector)
	s.check("edge_routes_variants", j.edgeRoutes)
	if !s.step("start_experiment", j.startExperiment) {
		return
	}
	if !s.step("traffic_and_conversions", j.traffic) {
		return
	}
	s.check("results_count_exposures_and_conversions", j.results)
	s.check("holdout_refused_while_running", j.holdoutRefused)
	s.check("promote_serves_variant", j.promote)
	s.check("layer_and_holdout", j.layerAndHoldout)
	s.check("kill_serves_fallback", j.kill)
	s.check("product_analytics_events", j.analytics)
	s.check("saved_insight_run_and_export", j.savedInsight)
}

// skipFlagsUnreleased skips the journey when the account cannot use Flags.
// A project that does not exist answers 404 when Flags are available.
func skipFlagsUnreleased(t *testing.T, tg *target) {
	t.Helper()
	res := tg.run(t, time.Minute, "flag", "list", "--project", tg.slug("no-such-project"))
	if res.exit != 0 && res.problemCode() != "not_found" {
		t.Skipf("Flags are not available to this account: %s", res.problem())
	}
}

func (j *flagsJourney) deploy(t *testing.T) {
	tg := j.tg
	probe, ok := flagsProbes[j.runtime]
	if !ok {
		t.Fatalf("no flags probe for runtime %q", j.runtime)
	}
	src := fixtureSource(t, probe.fixture)
	probe.addSDK(t, filepath.Join(src, "web"))
	config, _ := json.Marshal(map[string]string{"apiURL": tg.api})
	files := map[string][]byte{
		filepath.Join("web", "config.json"): config,
		// Flags are read by project apps: a one-service Compose project.
		"compose.yaml": []byte(fmt.Sprintf("services:\n  %s:\n    build: ./web\n    ports: [\"8080:8080\"]\n", j.app)),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(src, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	tg.deploySlots <- struct{}{}
	defer func() { <-tg.deploySlots }()
	res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy", "--project", "--project-slug", j.project,
		"--path", src, "--wait", "--timeout", deployLimit.String(), "--yes")
	if res.exit != 0 {
		t.Fatalf("project deploy failed (exit %d): %s\n%s", res.exit, tg.redact(res.problem()), tg.redact(tail(string(res.stdout)+string(res.stderr), 2000)))
	}
	// Project apps take the plan's auth default; customers call with
	// consumer keys, which the gateway resolves either way.
	tg.ok(t, time.Minute, "app", j.app, "--no-require-authn")
	j.base = tg.appURL(j.app)
	eventually(t, j.app+" serving", 3*time.Minute, 3*time.Second, func() (bool, string) {
		r := get(j.base + "/")
		return r.status == http.StatusOK && str(jsonBody(t, r), "probe") == "gregale-e2e-flags", r.String()
	})
}

// makeCustomers registers flagsCustomers platform tenants, each linked to a
// consumer of the app with a key. Tenants are keyed by a stable reference,
// so runs reuse them instead of adding new ones.
func (j *flagsJourney) makeCustomers(t *testing.T) {
	tg := j.tg
	for i := range j.count {
		ref := fmt.Sprintf("e2ej-flags-customer-%d", i)
		if j.runtime != "node" {
			// Parallel journeys never add the same tenant at once.
			ref = fmt.Sprintf("e2ej-flags-%s-customer-%d", j.runtime, i)
		}
		tenant := str(object(t, tg.ok(t, time.Minute, "api", "tenant", "add", "--external-ref", ref, "--name", "E2E flags customer "+fmt.Sprint(i)).stdout), "id")
		consumer := str(object(t, tg.ok(t, time.Minute, "api", "consumer", "create", j.app, "--external-ref", ref, "--name", ref).stdout), "id")
		if tenant == "" || consumer == "" {
			t.Fatalf("customer %d: tenant %q consumer %q", i, tenant, consumer)
		}
		tg.ok(t, time.Minute, "api", "tenant", "consumer", "link", "--id", tenant, "--consumer-id", consumer)
		out := tg.ok(t, time.Minute, "api", "consumer", "key", "create", j.app, consumer, "--name", tg.slug(fmt.Sprintf("flags-%s-key-%d", j.runtime, i)), "--scopes", "read")
		key := consumerKeyRe.FindString(string(out.stdout))
		if key == "" {
			t.Fatalf("customer %d: key create printed no ck_ key", i)
		}
		tg.addSensitive(key)
		j.customers = append(j.customers, &flagsCustomer{tenant: tenant, consumer: consumer, key: key})
	}
	// The gateway resolves the key to the tenant: the app sees it.
	c := j.customers[0]
	eventually(t, "a consumer key carries its verified customer", 2*time.Minute, 3*time.Second, func() (bool, string) {
		r := j.call(c, "/")
		return r.status == http.StatusOK && str(jsonBody(t, r), "customer") == c.tenant, r.String()
	})
}

// call sends one request to the app as customer c.
func (j *flagsJourney) call(c *flagsCustomer, path string) httpResult {
	return fetch(http.MethodGet, j.base+path, map[string]string{"Authorization": "Bearer " + c.key})
}

func (j *flagsJourney) flag(t *testing.T, args ...string) cliResult {
	t.Helper()
	return j.tg.ok(t, time.Minute, append(append([]string{"flag"}, args...), "--project", j.project)...)
}

func (j *flagsJourney) createFlag(t *testing.T) {
	j.flag(t, "create", "checkout", "--variants", "legacy,new", "--owner", "e2e-journeys")
	j.flag(t, "rollout", "set", "checkout", "100")
}

// rule is the checkout flag's weighted rule, which `flag rollout set` adds.
func (j *flagsJourney) rule(t *testing.T) string {
	t.Helper()
	doc := object(t, j.flag(t, "get").stdout)
	flags, _ := field(doc, "flags").([]any)
	for _, f := range flags {
		if str(f, "key") != "checkout" {
			continue
		}
		if rules, _ := field(f, "rules").([]any); len(rules) > 0 {
			return str(rules[0], "id")
		}
	}
	t.Fatalf("checkout has no rule: %s", compact(doc))
	return ""
}

// served is the variant the app serves customer c once it has the flag.
func (j *flagsJourney) served(t *testing.T, c *flagsCustomer, path string, want func(map[string]any) bool) map[string]any {
	t.Helper()
	var doc map[string]any
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the app's SDK view: %s", j.tg.redact(j.call(c, "/debug").String()))
		}
	})
	eventually(t, "the app serves "+c.tenant, 2*time.Minute, 3*time.Second, func() (bool, string) {
		r := j.call(c, path)
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc = jsonBody(t, r)
		return want(doc), compact(doc)
	})
	return doc
}

// sdkMatchesInspector checks that the SDK in the VM and the server's
// decision inspector agree for every customer, once the SDK holds the
// configuration version the inspector explains (it refreshes every 2 s, so
// it may still serve the version before `flag rollout set`).
func (j *flagsJourney) sdkMatchesInspector(t *testing.T) {
	for _, c := range j.customers {
		inspect := object(t, j.flag(t, "inspect", "--key", "checkout", "--customer-id", c.tenant, "--fallback-variant", "fallback").stdout)
		want := str(inspect, "value")
		if want == "" {
			want = str(inspect, "decision.value")
		}
		version := num(inspect, "config_version")
		doc := j.served(t, c, "/checkout", func(d map[string]any) bool {
			return str(d, "reason") != "" && str(d, "variant") != "fallback" && num(d, "config_version") >= version
		})
		if str(doc, "variant") != want {
			t.Errorf("customer %s: the SDK served %q, the inspector explains %q (%s)", c.tenant, str(doc, "variant"), want, compact(inspect))
		}
	}
}

// edgeRoutes routes /page at the edge (ADR-20261124212701): each verified
// customer gets the page of the variant `flag inspect` explains, without any
// flag code in the app, and an anonymous visitor keeps its page through its
// visitor cookie. It skips when edge routing is not released to the account.
func (j *flagsJourney) edgeRoutes(t *testing.T) {
	res := j.tg.run(t, time.Minute, "flag", "edge", "set", "checkout", "--path", "/page", "--variant", "new", "--rewrite", "/page-b", "--project", j.project)
	if res.exit != 0 {
		if strings.Contains(res.problem(), "Edge flags unavailable") || strings.Contains(res.problem(), "edge flag routing is not enabled") {
			t.Skipf("edge flag routing is not enabled for this account: %s", res.problem())
		}
		t.Fatalf("flag edge set failed (exit %d): %s", res.exit, res.problem())
	}
	t.Cleanup(func() { j.tg.run(t, time.Minute, "flag", "edge", "delete", "checkout", "--project", j.project) })
	for _, c := range j.customers {
		inspect := object(t, j.flag(t, "inspect", "--key", "checkout", "--customer-id", c.tenant, "--fallback-variant", "legacy").stdout)
		want := "a"
		if str(inspect, "value") == "new" {
			want = "b"
		}
		eventually(t, "the edge serves "+c.tenant+" page "+want, 2*time.Minute, 3*time.Second, func() (bool, string) {
			r := j.call(c, "/page")
			if r.status != http.StatusOK {
				return false, r.String()
			}
			return str(jsonBody(t, r), "page") == want, r.String()
		})
	}
	first := get(j.base + "/page")
	if first.status != http.StatusOK {
		t.Fatalf("anonymous /page: %s", first.String())
	}
	var visitor string
	for _, raw := range first.header.Values("Set-Cookie") {
		if name, rest, ok := strings.Cut(raw, "="); ok && name == "__Host-gregale_vid" {
			visitor, _, _ = strings.Cut(rest, ";")
		}
	}
	if visitor == "" {
		t.Fatalf("anonymous /page set no visitor cookie: %v", first.header.Values("Set-Cookie"))
	}
	page := str(jsonBody(t, first), "page")
	for i := 0; i < 3; i++ {
		again := fetch(http.MethodGet, j.base+"/page", map[string]string{"Cookie": "__Host-gregale_vid=" + visitor})
		if again.status != http.StatusOK || str(jsonBody(t, again), "page") != page {
			t.Fatalf("returning visitor got %s; first visit got page %s", again.String(), page)
		}
	}
}

func (j *flagsJourney) startExperiment(t *testing.T) {
	j.flag(t, "experiment", "create", "checkout-exp", "--flag", "checkout", "--rule", j.rule(t), "--control", "legacy",
		"--metric", "order_completed", "--no-guardrails", "--start")
}

// traffic sends each customer to checkout; every other customer converts.
func (j *flagsJourney) traffic(t *testing.T) {
	for i, c := range j.customers {
		path := "/checkout"
		c.converted = i%2 == 0
		if c.converted {
			path += "?convert=1"
		}
		doc := j.served(t, c, path, func(d map[string]any) bool { return str(d, "variant") == "legacy" || str(d, "variant") == "new" })
		c.variant = str(doc, "variant")
		if c.converted && field(doc, "tracked") != true {
			t.Errorf("customer %s: track() was dropped (%s)", c.tenant, compact(doc))
		}
	}
}

// results waits for the reconciler to count every customer's exposure in
// the arm it was served and every conversion.
func (j *flagsJourney) results(t *testing.T) {
	wantExposed := map[string]int{}
	wantConverted := map[string]int{}
	for _, c := range j.customers {
		wantExposed[c.variant]++
		if c.converted {
			wantConverted[c.variant]++
		}
	}
	eventually(t, "results count every exposure and conversion", 25*time.Minute, 30*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "--json", "flag", "experiment", "result", "get", "checkout-exp", "--project", j.project)
		if res.exit != 0 {
			return false, res.problem()
		}
		doc := object(t, res.stdout)
		arms := []any{field(doc, "control")}
		variants, _ := field(doc, "variants").([]any)
		arms = append(arms, variants...)
		for _, a := range arms {
			v := str(a, "variant")
			if int(num(a, "exposed")) != wantExposed[v] || int(num(a, "converted")) != wantConverted[v] {
				return false, fmt.Sprintf("arm %s exposed %v converted %v, want %d and %d", v, num(a, "exposed"), num(a, "converted"), wantExposed[v], wantConverted[v])
			}
		}
		return num(doc, "crossed_units") == 0, compact(doc)
	})
}

func (j *flagsJourney) holdoutRefused(t *testing.T) {
	res := j.tg.run(t, time.Minute, "flag", "experiment", "holdout", "set", "5", "--project", j.project)
	if res.exit == 0 || res.problemCode() != "flag_experiment_running" {
		t.Errorf("setting the holdout while an experiment runs = exit %d %s, want 409 flag_experiment_running", res.exit, res.problem())
	}
}

// promote ships the new variant: every customer gets it.
func (j *flagsJourney) promote(t *testing.T) {
	j.flag(t, "experiment", "promote", "checkout-exp", "new")
	for _, c := range j.customers {
		j.served(t, c, "/checkout", func(d map[string]any) bool { return str(d, "variant") == "new" })
	}
}

// layerAndHoldout manages a layer and the holdout once no experiment runs.
func (j *flagsJourney) layerAndHoldout(t *testing.T) {
	j.flag(t, "experiment", "layer", "create", "checkout-layer")
	list := object(t, j.flag(t, "experiment", "layer", "list").stdout)
	if !strings.Contains(compact(list), `"key":"checkout-layer"`) || !strings.Contains(compact(list), `"free":10000`) {
		t.Errorf("layer list = %s, want checkout-layer with every basis point free", compact(list))
	}
	j.flag(t, "experiment", "layer", "delete", "checkout-layer")
	j.flag(t, "experiment", "holdout", "set", "5")
	if h := object(t, j.flag(t, "experiment", "holdout", "get").stdout); num(h, "basis_points") != 500 {
		t.Errorf("holdout = %s, want 500 basis points", compact(h))
	}
	j.flag(t, "experiment", "holdout", "set", "0")
}

// kill serves the code's fallback to everyone, and unkill restores it.
func (j *flagsJourney) kill(t *testing.T) {
	c := j.customers[0]
	j.flag(t, "kill", "set", "checkout")
	j.served(t, c, "/checkout", func(d map[string]any) bool { return str(d, "variant") == "fallback" && str(d, "reason") == "killed" })
	j.flag(t, "kill", "unset", "checkout")
	j.served(t, c, "/checkout", func(d map[string]any) bool { return str(d, "variant") == "new" })
}

// analytics reads the tracked conversions back when product analytics is on
// for the account.
func (j *flagsJourney) analytics(t *testing.T) {
	converted := 0
	for _, c := range j.customers {
		if c.converted {
			converted++
		}
	}
	eventually(t, "order_completed events are queryable", 5*time.Minute, 15*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "--json", "insight", "event", "list", "order_completed", "--project", j.project)
		if res.exit != 0 {
			// Off for the account, the insights routes refuse (403); off
			// everywhere, the capability gate hides them (404 no such route).
			if strings.Contains(res.problem(), "product analytics") || strings.Contains(res.problem(), "no such API route") {
				t.Skipf("product analytics is not enabled for this account: %s", res.problem())
			}
			return false, res.problem()
		}
		doc := object(t, res.stdout)
		units := 0.0
		points, _ := field(doc, "points").([]any)
		for _, p := range points {
			units += num(p, "units")
		}
		return units == float64(converted), compact(doc)
	})
}

// savedInsight saves the conversion event as an insight (ADR-20261122190000),
// runs it as a teammate would and exports it as CSV.
func (j *flagsJourney) savedInsight(t *testing.T) {
	converted := 0
	for _, c := range j.customers {
		if c.converted {
			converted++
		}
	}
	res := j.tg.run(t, time.Minute, "--json", "insight", "saved", "create", "orders", "--kind", "events", "--event", "order_completed",
		"--days", "1", "--project", j.project)
	if res.exit != 0 {
		// Off for the account the routes refuse (403); off everywhere the
		// capability gate hides them (404 no such route).
		if strings.Contains(res.problem(), "unavailable") || strings.Contains(res.problem(), "no such API route") {
			t.Skipf("saved insights are not enabled for this account: %s", res.problem())
		}
		t.Fatalf("save insight: %s", res.problem())
	}
	t.Cleanup(func() { j.tg.run(t, time.Minute, "insight", "saved", "delete", "orders", "--project", j.project) })
	run := j.tg.run(t, time.Minute, "--json", "insight", "saved", "run", "orders", "--project", j.project)
	if run.exit != 0 {
		t.Fatalf("run saved insight: %s", run.problem())
	}
	if got := num(object(t, run.stdout), "value"); got < float64(converted) {
		t.Fatalf("saved insight value = %v, want at least the %d conversions: %s", got, converted, run.stdout)
	}
	csv := j.tg.run(t, time.Minute, "insight", "saved", "run", "orders", "--format", "csv", "--project", j.project)
	if csv.exit != 0 || !strings.HasPrefix(string(csv.stdout), "start,breakdown,events,units,value\n") {
		t.Fatalf("export saved insight: %s %q", csv.problem(), csv.stdout)
	}
}
