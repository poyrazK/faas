package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// labelsYAML declares the web app's labels in its manifest. It holds no
// run id, so the variant's source digest stays fixed and builds once.
const labelsYAML = `labels:
  tier: web
  owner: journeys
`

// TestResourceLabels is the labels and selectors journey
// (ADR-20261031100025): a deploy applies the manifest's labels, the CLI sets
// and unsets labels on an app, a cron and a job, and `app list`, `app wait`,
// `domain list`, `cron list` and `job list` take a selector, as does
// `GET /v1/apps`, which refuses an invalid one. Every selector carries the
// run's own label so other apps on the account never match. resource-labels
// is a preview released per account: the journey skips on accounts it is
// not released to. Managed databases carry labels the same way; they are
// left out because a database holds the account's only database slot on
// most plans.
func TestResourceLabels(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	skipHidden(t, hiddenCapabilities(t, tg), "resource-labels")
	run := "e2ej-run=" + tg.runID
	web := tg.app(t, "labels-web")
	apiApp := tg.app(t, "labels-api")
	s := newStory(t)

	if !s.step("manifest_labels_applied", func(t *testing.T) {
		tg.deploy(t, web, "--path", probeVariant(t, "v1-labels", map[string]string{"gregale.yaml": labelsYAML}))
		got := appLabels(t, tg, web)
		if got["tier"] != "web" || got["owner"] != "journeys" {
			t.Fatalf("the deploy did not apply the manifest's labels: %v", got)
		}
	}) {
		return
	}
	if !s.step("set", func(t *testing.T) {
		if res := tg.exec(t, tg.token, t.TempDir(), time.Minute, "deploy", "--create-only", "--app", "--name", apiApp); res.exit != 0 {
			t.Fatalf("deploy --create-only %s failed (exit %d): %s", apiApp, res.exit, tg.redact(res.problem()))
		}
		got := labelsOut(t, tg.ok(t, time.Minute, "app", "label", "set", web, run, "team=labels", "canary="))
		want := map[string]string{"e2ej-run": tg.runID, "team": "labels", "canary": "", "tier": "web", "owner": "journeys"}
		if !sameLabels(got, want) {
			t.Fatalf("labels after set = %v, want %v", got, want)
		}
		tg.ok(t, time.Minute, "app", "label", "set", apiApp, run, "team=labels", "tier=api")
		if doc := object(t, tg.ok(t, time.Minute, "app", apiApp).stdout); str(doc, "labels.tier") != "api" {
			t.Fatalf("the app view carries no labels: %s", compact(doc))
		}
	}) {
		return
	}
	s.check("unset", func(t *testing.T) {
		for range 2 { // unset is idempotent: a second run changes nothing
			got := labelsOut(t, tg.ok(t, time.Minute, "app", "label", "unset", web, "canary"))
			if _, ok := got["canary"]; ok || got["team"] != "labels" {
				t.Fatalf("labels after unset = %v", got)
			}
		}
	})
	s.check("limit_refused", func(t *testing.T) {
		// The CLI checks one change; the per-resource total is the server's.
		before := appLabels(t, tg, web)
		args := []string{"app", "label", "set", web}
		for i := range 30 {
			args = append(args, fmt.Sprintf("extra-%02d=x", i))
		}
		res := tg.run(t, time.Minute, args...)
		if res.exit == 0 || res.problemCode() != "label_limit_exceeded" {
			t.Fatalf("%d labels on one app: exit %d, %s", len(before)+30, res.exit, res.problem())
		}
		if got := appLabels(t, tg, web); !sameLabels(got, before) {
			t.Fatalf("a refused change still wrote labels: %v", got)
		}
	})
	s.check("app_list_selector", func(t *testing.T) {
		for _, tc := range []struct {
			selector string
			want     []string
		}{
			{run + ",tier in (web)", []string{web}},
			{run + ",tier notin (web)", []string{apiApp}},
			{run + ",team=labels", []string{apiApp, web}},
			{run + ",!team", nil},
		} {
			if got := slugsOf(objects(t, tg.ok(t, time.Minute, "app", "list", "-l", tc.selector).stdout), "slug"); !slices.Equal(got, tc.want) {
				t.Errorf("app list -l %q = %v, want %v", tc.selector, got, tc.want)
			}
		}
	})
	s.check("api_selector", func(t *testing.T) {
		key := tg.scopedKey(t, "labels-key", "apps:read")
		r := tg.apiCall(http.MethodGet, "/v1/apps?selector="+url.QueryEscape(run+",tier=api"), nil, key)
		if r.status != http.StatusOK {
			t.Fatalf("GET /v1/apps?selector: %s", r.String())
		}
		var apps []map[string]any
		if err := json.Unmarshal(r.body, &apps); err != nil {
			t.Fatalf("decode GET /v1/apps: %v (%s)", err, r)
		}
		if got := slugsOf(apps, "slug"); !slices.Equal(got, []string{apiApp}) {
			t.Fatalf("the server's own match = %v, want [%s]", got, apiApp)
		}
		bad := tg.apiCall(http.MethodGet, "/v1/apps?selector="+url.QueryEscape("tier in (web"), nil, key)
		if bad.status != http.StatusBadRequest || str(jsonBody(t, bad), "code") != "invalid_label_selector" {
			t.Fatalf("an invalid selector: %s", bad.String())
		}
	})
	s.check("app_wait_selector", func(t *testing.T) {
		tg.ok(t, 4*time.Minute, "app", "wait", "-l", run+",tier=web", "--for", "Ready", "--timeout", "3m")
		res := tg.run(t, time.Minute, "app", "wait", "-l", run+",tier=nothing", "--timeout", "10s")
		if res.exit != 4 {
			t.Fatalf("a wait that picks no app: exit %d, want 4: %s", res.exit, res.problem())
		}
	})
	s.check("domain_list_selector", func(t *testing.T) {
		// The run's apps have no custom domain, so a selected list is empty
		// whatever other apps on the account hold.
		if got := objects(t, tg.ok(t, time.Minute, "domain", "list", "-l", run).stdout); len(got) != 0 {
			t.Fatalf("domain list -l %q listed another app's domains: %v", run, got)
		}
	})
	s.check("cron_labels", func(t *testing.T) {
		created := object(t, tg.ok(t, 2*time.Minute, "cron", "create", "--app", web,
			"--schedule", "0 0 1 1 *", "--path", "/cron").stdout)
		id := str(created, "id")
		if id == "" {
			t.Fatalf("cron create printed no id: %s", compact(created))
		}
		t.Cleanup(func() { tg.run(t, time.Minute, "cron", "delete", id) })
		got := labelsOut(t, tg.ok(t, time.Minute, "cron", "label", "set", id, run, "owner=labels"))
		if got["owner"] != "labels" {
			t.Fatalf("cron labels after set = %v", got)
		}
		if listed := slugsOf(objects(t, tg.ok(t, time.Minute, "cron", "list", "-l", run).stdout), "id"); !slices.Equal(listed, []string{id}) {
			t.Fatalf("cron list -l %q = %v, want [%s]", run, listed, id)
		}
		if listed := objects(t, tg.ok(t, time.Minute, "cron", "list", "-l", run+",owner=nobody").stdout); len(listed) != 0 {
			t.Fatalf("cron list matched a selector the cron fails: %v", listed)
		}
	})
	s.check("job_labels", func(t *testing.T) {
		name := tg.slug("labels-job")
		t.Cleanup(func() {
			if res := tg.run(t, time.Minute, "job", "delete", name); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
				t.Logf("warning: cleanup could not delete job %s (%s)", name, res.problem())
			}
		})
		tg.ok(t, 2*time.Minute, "job", "create", name, "--image", "alpine:3.20", "--", "/bin/true")
		tg.ok(t, time.Minute, "job", "label", "set", name, run, "tier=batch")
		tg.ok(t, time.Minute, "job", "label", "unset", name, "tier")
		if got := labelsOut(t, tg.ok(t, time.Minute, "job", "label", "list", name)); !sameLabels(got, map[string]string{"e2ej-run": tg.runID}) {
			t.Fatalf("job labels = %v", got)
		}
		doc := object(t, tg.ok(t, time.Minute, "job", "list", "-l", run, "--all").stdout)
		jobs, _ := field(doc, "jobs").([]any)
		var listed []string
		for _, j := range jobs {
			listed = append(listed, str(j, "name"))
		}
		if !slices.Equal(listed, []string{name}) {
			t.Fatalf("job list -l %q = %v, want [%s]", run, listed, name)
		}
	})
}

// appLabels reads an app's labels through `app label list`.
func appLabels(t *testing.T, tg *target, slug string) map[string]string {
	t.Helper()
	return labelsOut(t, tg.ok(t, time.Minute, "app", "label", "list", slug))
}

// labelsOut decodes a `label list|set|unset` result: {"labels": {...}}.
func labelsOut(t *testing.T, res cliResult) map[string]string {
	t.Helper()
	m, _ := field(object(t, res.stdout), "labels").(map[string]any)
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, _ := v.(string)
		out[k] = s
	}
	return out
}

func sameLabels(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			return false
		}
	}
	return true
}

// slugsOf returns the sorted values of key in docs.
func slugsOf(docs []map[string]any, key string) []string {
	var out []string
	for _, d := range docs {
		out = append(out, str(d, key))
	}
	slices.Sort(out)
	return out
}
