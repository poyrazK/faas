package deploydiff

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestCompute_PointerAwareAppConfig — the wire form's nil-vs-explicit
// distinction must survive the diff. *int(nil) = "don't touch";
// *int(&v) = "set to v". This is the contract per
// [pr-819-openapi-nullable-3-1] in memory.
func TestCompute_PointerAwareAppConfig(t *testing.T) {
	base := &api.AppResponse{
		Slug: "api", RAMMB: 256, MaxConcurrency: 2,
		StreamingEnabled: true, RequireAuthn: false,
	}
	baseline := Baseline{App: base}

	t.Run("nil fields produce no changes", func(t *testing.T) {
		got := Compute("api", "", baseline, Pending{})
		if len(got.Changes) != 0 {
			t.Fatalf("nil fields should produce 0 changes; got %+v", got.Changes)
		}
	})

	t.Run("non-nil equal value produces no change", func(t *testing.T) {
		v := 256
		got := Compute("api", "", baseline, Pending{
			AppConfig: AppConfigPatch{RAMMB: &v},
		})
		if len(got.Changes) != 0 {
			t.Fatalf("equal value should produce 0 changes; got %+v", got.Changes)
		}
	})

	t.Run("non-nil different value produces a Change", func(t *testing.T) {
		v := 512
		got := Compute("api", "", baseline, Pending{
			AppConfig: AppConfigPatch{RAMMB: &v},
		})
		if len(got.Changes) != 1 {
			t.Fatalf("expected 1 change; got %d", len(got.Changes))
		}
		c := got.Changes[0]
		if c.Field != "memory" || c.Kind != ChangeModify {
			t.Fatalf("unexpected change: %+v", c)
		}
		if c.Before.Value != 256 || c.After.Value != 512 {
			t.Fatalf("before/after wrong: before=%v after=%v", c.Before.Value, c.After.Value)
		}
	})

	t.Run("explicit false on boolean is not nil", func(t *testing.T) {
		f := false
		got := Compute("api", "", baseline, Pending{
			AppConfig: AppConfigPatch{StreamingEnabled: &f},
		})
		if len(got.Changes) != 1 {
			t.Fatalf("expected 1 change for streaming_enabled: false→true→false; got %d", len(got.Changes))
		}
	})
}

// TestCompute_FreshApp — every non-nil Pending field is a new-value
// Change when the app does not exist yet.
func TestCompute_FreshApp(t *testing.T) {
	v := 256
	c := 2
	got := Compute("new-app", "", Baseline{}, Pending{
		AppConfig: AppConfigPatch{RAMMB: &v, MaxConcurrency: &c},
	})
	if len(got.Changes) != 2 {
		t.Fatalf("expected 2 add-changes; got %d: %+v", len(got.Changes), got.Changes)
	}
	for _, ch := range got.Changes {
		if ch.Kind != ChangeAdd {
			t.Fatalf("fresh-app changes should be Add; got %s for %s", ch.Kind, ch.Field)
		}
	}
}

func TestCompute_FreshSourcePreviewIncludesAppAndDeploymentIdentity(t *testing.T) {
	got := Compute("new-function", api.PlanHobby, Baseline{}, Pending{
		BuildPlan: &api.BuildPlan{
			Framework:    "python",
			Runtime:      "python312",
			Handler:      "handler.handler",
			Class:        "function",
			SourceSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	})

	if len(got.Changes) != 2 {
		t.Fatalf("fresh source preview changes = %d, want app + deployment: %+v", len(got.Changes), got.Changes)
	}
	var appChange, deploymentChange *Change
	for i := range got.Changes {
		switch got.Changes[i].Field {
		case "app":
			appChange = &got.Changes[i]
		case "deployment":
			deploymentChange = &got.Changes[i]
		}
	}
	if appChange == nil || appChange.Kind != ChangeAdd {
		t.Fatalf("fresh app change = %+v, want add", appChange)
	}
	if deploymentChange == nil || deploymentChange.Kind != ChangeAdd {
		t.Fatalf("fresh deployment change = %+v, want add", deploymentChange)
	}
	app, ok := appChange.After.Value.(map[string]string)
	if !ok || app["class"] != "function" || app["runtime"] != "python312" || app["handler"] != "handler.handler" || app["framework"] != "python" {
		t.Fatalf("app change payload = %#v, want resolved function plan", appChange.After.Value)
	}
	dep, ok := deploymentChange.After.Value.(map[string]string)
	if !ok || dep["source_sha256"] == "" {
		t.Fatalf("deployment change payload = %#v, want source identity", deploymentChange.After.Value)
	}
}

func TestCompute_FreshImagePreviewIncludesDeploymentIdentity(t *testing.T) {
	got := Compute("new-image", api.PlanHobby, Baseline{}, Pending{
		BuildPlan: &api.BuildPlan{Class: "app", Framework: "unknown"},
		ImageRef:  "registry.example.com/hello:v1",
	})

	var deploymentChange *Change
	for i := range got.Changes {
		if got.Changes[i].Field == "deployment" {
			deploymentChange = &got.Changes[i]
		}
	}
	if deploymentChange == nil || deploymentChange.Kind != ChangeAdd {
		t.Fatalf("fresh image deployment change = %+v, want add", deploymentChange)
	}
	dep, ok := deploymentChange.After.Value.(map[string]string)
	if !ok || dep["image"] != "registry.example.com/hello:v1" {
		t.Fatalf("deployment change payload = %#v, want image identity", deploymentChange.After.Value)
	}
}

func TestCompute_ExistingAppWithoutDeploymentIncludesFirstDeployment(t *testing.T) {
	cases := []struct {
		name      string
		appType   string
		pending   Pending
		wantField string
		wantValue string
	}{
		{
			name:      "source function",
			appType:   "function",
			pending:   Pending{BuildPlan: &api.BuildPlan{Class: "function", SourceSHA256: "source-sha"}},
			wantField: "source_sha256",
			wantValue: "source-sha",
		},
		{
			name:      "image app",
			appType:   "app",
			pending:   Pending{BuildPlan: &api.BuildPlan{Class: "app"}, ImageRef: "registry.example.com/app@sha256:abc"},
			wantField: "image",
			wantValue: "registry.example.com/app@sha256:abc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute("reserved", api.PlanHobby, Baseline{
				App: &api.AppResponse{Slug: "reserved", Type: tc.appType},
			}, tc.pending)

			var deploymentAdd *Change
			for i := range got.Changes {
				change := &got.Changes[i]
				if change.Field == "deployment" && change.Kind == ChangeAdd {
					deploymentAdd = change
				}
				if change.Field == "deployment.source_sha256" || change.Field == "deployment.image" {
					t.Fatalf("first deployment identity was rendered as a field modification: %+v", change)
				}
			}
			if deploymentAdd == nil {
				t.Fatalf("changes = %+v, want first deployment add", got.Changes)
			}
			values, ok := deploymentAdd.After.Value.(map[string]string)
			if !ok || values[tc.wantField] != tc.wantValue {
				t.Fatalf("deployment add payload = %#v, want %s=%q", deploymentAdd.After.Value, tc.wantField, tc.wantValue)
			}
		})
	}
}

// TestCompute_EnvByScope — per-scope env diff: add, remove, modify.
func TestCompute_EnvByScope(t *testing.T) {
	baseline := Baseline{
		EnvByScope: map[string][]string{
			"default": {"FOO", "BAR"},
		},
	}
	pending := Pending{
		EnvByScope: map[string][]PendingEnv{
			"default": {{Key: "BAR"}, {Key: "BAZ", Value: "z"}},
			"staging": {{Key: "DEBUG", Value: "1"}},
		},
	}
	got := Compute("api", "", baseline, pending)

	// FOO removed, BAZ added (default), DEBUG added (staging).
	wantAdds := map[string]bool{
		"environment.default.BAZ":   false,
		"environment.staging.DEBUG": false,
	}
	wantRemoves := map[string]bool{
		"environment.default.FOO": false,
	}
	for _, c := range got.Changes {
		switch c.Kind {
		case ChangeAdd:
			if _, ok := wantAdds[c.Field]; !ok {
				t.Fatalf("unexpected add: %s", c.Field)
			}
			wantAdds[c.Field] = true
		case ChangeRemove:
			if _, ok := wantRemoves[c.Field]; !ok {
				t.Fatalf("unexpected remove: %s", c.Field)
			}
			wantRemoves[c.Field] = true
		}
	}
	for k, seen := range wantAdds {
		if !seen {
			t.Fatalf("missing add: %s", k)
		}
	}
	for k, seen := range wantRemoves {
		if !seen {
			t.Fatalf("missing remove: %s", k)
		}
	}
}

// TestCompute_Crons — (schedule, path) unique key per migration 00210.
func TestCompute_Crons(t *testing.T) {
	baseline := Baseline{
		Crons: []api.CronResponse{
			{ID: "c1", Schedule: "* * * * *", Path: "/old", Enabled: true},
		},
	}
	pending := Pending{
		Crons: []api.CreateCronRequest{
			{Schedule: "* * * * *", Path: "/new"},
			{Schedule: "0 * * * *", Path: "/hourly"},
		},
	}
	got := Compute("api", "", baseline, pending)

	removed := false
	added1 := false
	added2 := false
	for _, c := range got.Changes {
		switch {
		case c.Kind == ChangeRemove && c.Field == "cron[* * * * * /old]":
			removed = true
		case c.Kind == ChangeAdd && c.Field == "cron[* * * * * /new]":
			added1 = true
		case c.Kind == ChangeAdd && c.Field == "cron[0 * * * * /hourly]":
			added2 = true
		}
	}
	if !removed || !added1 || !added2 {
		t.Fatalf("cron diff wrong: removed=%v added1=%v added2=%v", removed, added1, added2)
	}
}

// TestCompute_EdgeRules — (kind, host, path) is the stable identity;
// priority / methods / enabled / action flips are "modify".
func TestCompute_EdgeRules(t *testing.T) {
	mkAction := func(s string) json.RawMessage {
		b, _ := json.Marshal(map[string]string{"path": s})
		return b
	}
	baseline := Baseline{
		EdgeRules: []api.EdgeRuleResponse{
			{
				ID: "e1", MatchHost: "api.example.com", MatchPath: "/v1",
				MatchMethods: []string{"GET"}, Priority: 10, Enabled: true,
				Kind: "route", Action: mkAction("/v1"),
			},
		},
	}
	pending := Pending{
		EdgeRules: []api.CreateEdgeRuleRequest{
			{
				MatchHost: "api.example.com", MatchPath: "/v1",
				MatchMethods: []string{"GET", "POST"}, Priority: intPtr(5),
				Kind: "route", Action: mkAction("/v2"),
			},
			{
				MatchHost: "api.example.com", MatchPath: "/payments",
				Kind: "route", Action: mkAction("/payments"),
			},
		},
	}
	got := Compute("api", "", baseline, pending)

	modified := false
	added := false
	for _, c := range got.Changes {
		switch c.Field {
		case "edge_rule[route api.example.com/v1]":
			if c.Kind == ChangeModify {
				modified = true
			}
		case "edge_rule[route api.example.com/payments]":
			if c.Kind == ChangeAdd {
				added = true
			}
		}
	}
	if !modified || !added {
		t.Fatalf("edge rule diff wrong: modified=%v added=%v", modified, added)
	}
}

// TestCompute_DeploymentImmutable — image / manifest entrypoint /
// port / healthz changes emit a "would_create_deployment" Break,
// not a Change. Per dto.go:1326: deployment fields are immutable
// post-create except min_instances.
func TestCompute_DeploymentImmutable(t *testing.T) {
	base := &api.DeploymentResponse{
		ImageDigest:         "ghcr.io/me/api@sha256:old",
		OverrideEntrypoint:  []string{"/app/server"},
		OverridePort:        8080,
		OverrideHealthcheck: &api.DeploymentHealthcheck{Path: "/healthz"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}

	// Image change → Break.
	pending := Pending{
		ImageRef: "ghcr.io/me/api@sha256:new",
	}
	got := Compute("api", "", baseline, pending)
	found := false
	for _, b := range got.Breaks {
		if b.Code == "would_create_deployment" {
			found = true
		}
	}
	if !found {
		t.Fatalf("image change should emit would_create_deployment break; got %+v", got.Breaks)
	}

	// Entrypoint change → Break.
	pending = Pending{
		Manifest: &api.AppManifest{
			Entrypoint: []string{"/app/new-server"},
			Port:       8080,
			Healthz:    "/healthz",
		},
	}
	got = Compute("api", "", baseline, pending)
	found = false
	for _, b := range got.Breaks {
		if b.Code == "would_create_deployment" {
			found = true
		}
	}
	if !found {
		t.Fatalf("entrypoint change should emit would_create_deployment break; got %+v", got.Breaks)
	}
}

// TestCompute_DeploymentImmutable_HealthzClear — code-review
// finding #3: clearing the healthz path (Healthz:"" on the
// manifest when the deployment has healthz=/healthz) must still
// emit a would_create_deployment break. Earlier versions
// short-circuited on `p.Manifest.Healthz != ""` and silently
// dropped the break.
func TestCompute_DeploymentImmutable_HealthzClear(t *testing.T) {
	base := &api.DeploymentResponse{
		OverrideHealthcheck: &api.DeploymentHealthcheck{Path: "/healthz"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{Healthz: ""}, // explicitly clear
	})
	found := false
	for _, b := range got.Breaks {
		if b.Code == "would_create_deployment" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clearing the healthz path must emit would_create_deployment; got %+v", got.Breaks)
	}
}

// TestCompute_DeploymentImmutable_PortClear — code-review finding #3
// hardening: clearing the port override (reverting to the default
// by setting Manifest.Port=0) against a deployment with
// OverridePort=8080 must emit would_create_deployment. The pre-review
// guard `p.Manifest.Port != 0 && p.Manifest.Port != base.OverridePort`
// short-circuited when the manifest cleared the field, silently
// dropping the break and missing the customer-visible immutable
// change. This test is the parity sibling of
// TestCompute_DeploymentImmutable_HealthzClear above.
func TestCompute_DeploymentImmutable_PortClear(t *testing.T) {
	base := &api.DeploymentResponse{
		OverridePort: 8080,
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{Port: 0}, // explicitly clear (revert to default)
	})
	found := false
	for _, b := range got.Breaks {
		if b.Code == "would_create_deployment" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clearing the port override must emit would_create_deployment; got %+v", got.Breaks)
	}
}

// TestCompute_SchemaEnvChanged_KeyClear — code-review finding #4:
// clearing every env key from the manifest (Env == empty map) must
// still emit a schema_env_changed break when the baseline had
// keys. Earlier versions short-circuited on `len(p.Manifest.Env)
// > 0` and silently dropped the break.
func TestCompute_SchemaEnvChanged_KeyClear(t *testing.T) {
	base := &api.DeploymentResponse{
		OverrideEnvKeys: []string{"FOO", "BAR"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{Env: map[string]string{}}, // cleared
	})
	found := false
	for _, b := range got.Breaks {
		if b.Code == "schema_env_changed" && b.Field == "manifest.env" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clearing env keys must emit schema_env_changed; got %+v", got.Breaks)
	}
}

// TestCompute_EdgeRules_DuplicateKey — code-review finding #5:
// when the pending list carries the same (kind, host, path)
// tuple twice, the diff must surface an
// `edge_rule_duplicate_key` error-severity break (rather than
// silently overwriting in the key map and emitting one row).
// apid's CREATE UNIQUE constraint rejects the deploy at write
// time; the diff is the pre-deploy warning.
func TestCompute_EdgeRules_DuplicateKey(t *testing.T) {
	pending := []api.CreateEdgeRuleRequest{
		{
			MatchHost: "api.example.com", MatchPath: "/v1",
			Kind: "route", Action: mkAction("/v1"),
		},
		{
			MatchHost: "api.example.com", MatchPath: "/v1",
			Kind: "route", Action: mkAction("/v1/v2"), // same key, different action
		},
	}
	got := Compute("api", "", Baseline{}, Pending{EdgeRules: pending})
	dupCount := 0
	for _, b := range got.Breaks {
		if b.Code == "edge_rule_duplicate_key" {
			dupCount++
		}
	}
	if dupCount != 1 {
		t.Fatalf("expected 1 edge_rule_duplicate_key break; got %d in %+v", dupCount, got.Breaks)
	}
}

// TestCompute_HasBlockingBreaks — the gate's exit-1 input.
func TestCompute_HasBlockingBreaks(t *testing.T) {
	d := Diff{
		Breaks: []Break{
			{Code: "warn_only", Severity: "warn"},
		},
	}
	if d.HasBlockingBreaks() {
		t.Fatal("warn-only diff should not block")
	}
	d.Breaks = append(d.Breaks, Break{Code: "real", Severity: "error"})
	if !d.HasBlockingBreaks() {
		t.Fatal("error-severity break should block")
	}
}

// TestEnvByScopeFromList — the wire-shape helper. Both the flat
// list and the nested EnvByScope shape must round-trip.
func TestEnvByScopeFromList(t *testing.T) {
	t.Run("nested scope shape", func(t *testing.T) {
		got := EnvByScopeFromList(api.AppEnvListResponse{
			EnvByScope: api.EnvByScope{
				"default": []api.ScopedAppEnvResponse{
					{Key: "FOO"}, {Key: "BAR"},
				},
				"staging": []api.ScopedAppEnvResponse{
					{Key: "DEBUG"},
				},
			},
		})
		if len(got["default"]) != 2 || len(got["staging"]) != 1 {
			t.Fatalf("nested scope parse wrong: %+v", got)
		}
	})
	t.Run("flat shape falls back to default scope", func(t *testing.T) {
		got := EnvByScopeFromList(api.AppEnvListResponse{
			Env: []api.AppEnvResponse{{Key: "FOO"}, {Key: "BAR"}},
		})
		if len(got[api.DefaultEnvScope]) != 2 {
			t.Fatalf("flat shape should populate default scope: %+v", got)
		}
	})
}

// TestStringSliceEqual — order-independent equality.
func TestStringSliceEqual(t *testing.T) {
	if !stringSliceEqual([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatal("set equality should ignore order")
	}
	if stringSliceEqual([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("different lengths should not be equal")
	}
}

// mkAction builds a tiny json.RawMessage for edge-rule Action
// fields in tests.
func mkAction(s string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"path": s})
	return b
}

// intPtr returns &v; used for pointer-typed fields on
// api.CreateEdgeRuleRequest (Priority, Enabled).
func intPtr(v int) *int { return &v }

// TestCompute_SchemaBreak_StructuralRouteRemoved — PR-2:
// removing a route edge rule between baseline and pending must
// surface a schema_response_changed break anchored to the path.
// This pins the structural diff path that pkg/openapidiff opens:
// the engine wires in via detectStructuralSchemaBreak, which
// projects both edge-rule lists onto the embedded OpenAPI spec
// and emits one Break per SchemaBreak.
//
// Severity must be "error" (vs the PR-0 text-only path's
// "warn") — route removal is a wire-shape break customers
// must react to, not a behavioural hint.
func TestCompute_SchemaBreak_StructuralRouteRemoved(t *testing.T) {
	baseRules := []api.EdgeRuleResponse{
		{ID: "rule_1", Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo"},
	}
	baseline := Baseline{
		App:       &api.AppResponse{Slug: "api"},
		EdgeRules: baseRules,
	}
	// Pending: same manifest, but the route rule has been
	// removed. No new rule takes its place.
	pending := Pending{
		EdgeRules: nil,
	}
	got := Compute("api", "", baseline, pending)
	found := false
	for _, b := range got.Breaks {
		if b.Code == "schema_response_changed" &&
			b.Severity == SeverityError &&
			b.Field != "" && // path/method/status anchor
			contains(b.Field, "api.example.com/v1/foo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("removing a route edge rule must fire a structural schema_response_changed error break; got %+v", got.Breaks)
	}
}

// TestCompute_SchemaBreak_StructuralRouteAdded_NoBreak — PR-2:
// adding a NEW route edge rule does NOT fire a break (path
// adds are Diff.Changes, not Breaks). This pins the asymmetry:
// removing a route is a wire-shape break, adding one is a
// positive change.
func TestCompute_SchemaBreak_StructuralRouteAdded_NoBreak(t *testing.T) {
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}}
	pending := Pending{
		EdgeRules: []api.CreateEdgeRuleRequest{
			{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo"},
		},
	}
	got := Compute("api", "", baseline, pending)
	for _, b := range got.Breaks {
		if b.Code == "schema_response_changed" && b.Severity == SeverityError {
			t.Fatalf("adding a new route must NOT fire an error break; got %+v", b)
		}
	}
}

// TestCompute_SchemaBreak_TextOnlyStillFires — PR-2: the PR-0
// text-only schema_env_changed / entrypoint signals must
// still fire on the manifest-present path. The structural
// pass is additive, not a replacement.
func TestCompute_SchemaBreak_TextOnlyStillFires(t *testing.T) {
	base := &api.DeploymentResponse{
		OverrideEnvKeys: []string{"FOO"},
	}
	baseline := Baseline{App: &api.AppResponse{Slug: "api"}, LatestDeployment: base}
	got := Compute("api", "", baseline, Pending{
		Manifest: &api.AppManifest{
			Env: map[string]string{"BAR": "v"},
		},
	})
	found := false
	for _, b := range got.Breaks {
		if b.Code == "schema_env_changed" && b.Field == "manifest.env" {
			found = true
		}
	}
	if !found {
		t.Fatalf("text-only schema_env_changed must still fire when manifest env key set changes; got %+v", got.Breaks)
	}
}

// TestCompute_ScopeMismatch — SAFE-RELEASES Stream E. ADR-091's
// per-deployment scope targeting means a single app can have one
// live row per scope; the diff must surface cross-env promotions
// (default → staging) as informational so the operator can confirm
// they meant to promote, not patch the default row.
func TestCompute_ScopeMismatch(t *testing.T) {
	base := &api.DeploymentResponse{Scope: "default"}
	baseline := Baseline{
		App:              &api.AppResponse{Slug: "api"},
		LatestDeployment: base,
		LatestScope:      "default",
	}
	// Pending deployment targets staging instead of default.
	pending := Pending{Scope: "staging"}
	got := Compute("api", "", baseline, pending)
	found := false
	for _, b := range got.Breaks {
		if b.Code == "scope_mismatch" && b.Field == "deployment.scope" {
			found = true
			if b.Severity != SeverityWarn {
				t.Errorf("Severity = %q, want %q", b.Severity, SeverityWarn)
			}
		}
	}
	if !found {
		t.Fatalf("scope change must emit scope_mismatch break; got %+v", got.Breaks)
	}
}

// TestCompute_ScopeMismatch_NoBreakWhenEqual — same scope on
// both sides is a no-op.
func TestCompute_ScopeMismatch_NoBreakWhenEqual(t *testing.T) {
	base := &api.DeploymentResponse{Scope: "default"}
	baseline := Baseline{
		App:              &api.AppResponse{Slug: "api"},
		LatestDeployment: base,
		LatestScope:      "default",
	}
	pending := Pending{Scope: "default"}
	got := Compute("api", "", baseline, pending)
	for _, b := range got.Breaks {
		if b.Code == "scope_mismatch" {
			t.Errorf("scope_mismatch must not fire when both sides carry the same scope; got %+v", got.Breaks)
		}
	}
}

// TestCompute_ScopeMismatch_EmptyOnEitherSide — empty on
// either side is a no-op (deployments with no explicit scope
// default to "default" at write time; comparing against an
// empty string would fire a false positive on every deploy).
func TestCompute_ScopeMismatch_EmptyOnEitherSide(t *testing.T) {
	base := &api.DeploymentResponse{Scope: "default"}
	baseline := Baseline{
		App:              &api.AppResponse{Slug: "api"},
		LatestDeployment: base,
		LatestScope:      "default",
	}
	// Empty pending scope: handler coerces "" → "default" at
	// write time, so this must not fire scope_mismatch.
	pending := Pending{Scope: ""}
	got := Compute("api", "", baseline, pending)
	for _, b := range got.Breaks {
		if b.Code == "scope_mismatch" {
			t.Errorf("scope_mismatch must not fire when pending scope is empty; got %+v", got.Breaks)
		}
	}
}

// TestCompute_BaselineFieldsPopulate_NoDepsReadsToNil — pin the
// buildDiffBaseline wire-up: a baseline built from a non-nil
// LatestDeployment must populate LatestScope from dep.Scope so
// the engine can compare. This is the apid-side counterpart to
// the engine tests above — it asserts the server-side builder
// doesn't forget to mirror the column into the baseline.
func TestCompute_BaselineFieldsPopulate_NoDepsReadsToNil(t *testing.T) {
	base := &api.DeploymentResponse{
		Scope: "staging",
	}
	baseline := Baseline{
		App:              &api.AppResponse{Slug: "api"},
		LatestDeployment: base,
		LatestScope:      base.Scope,
	}
	if baseline.LatestScope != "staging" {
		t.Errorf("LatestScope = %q, want staging", baseline.LatestScope)
	}
}

// contains is a tiny substring helper; the test anchor uses
// string-contains rather than exact equality because the
// field string is path + method + status, and we only want
// to assert the path is in there.
func contains(haystack, needle string) bool {
	return len(needle) <= len(haystack) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
