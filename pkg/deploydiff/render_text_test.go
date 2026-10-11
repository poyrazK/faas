// Whitebox tests for pkg/deploydiff/render_text.go. The renderer's
// output is intentionally byte-stable so CI greps land on the right
// rows (per the doc on RenderText at render_text.go:17). These
// tests pin that contract.
//
// Pre-existing tests at engine_test.go + quota_test.go don't cover
// the renderer; this file is the full coverage pass.

package deploydiff

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// asAnyLit is a thin wrapper so AsAny sits behind a constructor the
// rest of the test file can call cleanly. AsAny wraps the value in
// anyJSON (MarshalJSON + json.Marshal-failure fallback) so the diff
// renderer emits printable strings.
func asAnyLit(v any) anyJSON { return AsAny(v) }

// renderTo is a one-line helper: write the diff to a buffer and
// return the bytes.
func renderTo(t *testing.T, d Diff) string {
	t.Helper()
	var buf bytes.Buffer
	RenderText(&buf, d)
	return buf.String()
}

// --- Top banner + section emission ----------------------------------

func TestRenderText_TopBanner(t *testing.T) {
	d := Diff{Slug: "api"}
	got := renderTo(t, d)
	if !strings.HasPrefix(got, "Deployment diff for \"api\"\n\n") {
		t.Errorf("top banner = %q, want prefix 'Deployment diff for \"api\"'", got)
	}
}

func TestRenderText_NoSlug_OmitsBanner(t *testing.T) {
	// Empty slug skips the banner (render_text.go:46-48).
	d := Diff{}
	got := renderTo(t, d)
	if strings.HasPrefix(got, "Deployment diff") {
		t.Errorf("banner should be omitted when slug is empty; got %q", got)
	}
}

func TestRenderText_AppConfigSection(t *testing.T) {
	// Hits isHeadlineScalar at render_text.go:174-182.
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "memory", Kind: ChangeModify, Before: asAnyLit(512), After: asAnyLit(256)},
			{Field: "concurrency", Kind: ChangeModify, Before: asAnyLit(2), After: asAnyLit(4)},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "App config:") {
		t.Errorf("missing 'App config:' section in:\n%s", got)
	}
	if !strings.Contains(got, "memory") {
		t.Errorf("missing 'memory' row in:\n%s", got)
	}
	if !strings.Contains(got, "concurrency") {
		t.Errorf("missing 'concurrency' row in:\n%s", got)
	}
	if !strings.Contains(got, "→") {
		t.Errorf("missing arrow for modify row in:\n%s", got)
	}
}

func TestRenderText_EnvironmentAddRemoveModify(t *testing.T) {
	// Exercises the env-row switch at render_text.go:84-91.
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "environment.default.NEW", Kind: ChangeAdd},
			{Field: "environment.default.OLD", Kind: ChangeRemove},
			{Field: "environment.default.MOD", Kind: ChangeModify,
				Before: asAnyLit("a"), After: asAnyLit("b")},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Environment:") {
		t.Errorf("missing Environment: in:\n%s", got)
	}
	if !strings.Contains(got, "+ environment.default.NEW") {
		t.Errorf("missing add row in:\n%s", got)
	}
	if !strings.Contains(got, "- environment.default.OLD") {
		t.Errorf("missing remove row in:\n%s", got)
	}
	if !strings.Contains(got, "~ environment.default.MOD") {
		t.Errorf("missing modify row in:\n%s", got)
	}
}

func TestRenderText_CronsSection(t *testing.T) {
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "cron[nightly:cleanup]", Kind: ChangeAdd},
			{Field: "cron[hourly:dump]", Kind: ChangeRemove},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Crons:") {
		t.Errorf("missing Crons: in:\n%s", got)
	}
	if !strings.Contains(got, "+ cron[nightly:cleanup]") {
		t.Errorf("missing cron add row in:\n%s", got)
	}
	if !strings.Contains(got, "- cron[hourly:dump]") {
		t.Errorf("missing cron remove row in:\n%s", got)
	}
}

func TestRenderText_EdgeRulesSection(t *testing.T) {
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "edge_rule[POST /things]", Kind: ChangeAdd},
			{Field: "edge_rule[DELETE /things]", Kind: ChangeModify},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Routes / edge rules:") {
		t.Errorf("missing Routes heading in:\n%s", got)
	}
	if !strings.Contains(got, "+ edge_rule[POST /things]") {
		t.Errorf("missing edge_rule add row in:\n%s", got)
	}
	if !strings.Contains(got, "~ edge_rule[DELETE /things]") {
		t.Errorf("missing edge_rule modify row in:\n%s", got)
	}
}

func TestRenderText_OtherSettingsSection(t *testing.T) {
	// A non-headline app-config field (e.g. autoscale_target_rps)
	// goes to "Other settings:" via otherScalars at render_text.go:187-201.
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "autoscale_target_rps", Kind: ChangeModify,
				Before: asAnyLit("10"), After: asAnyLit("20")},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Other settings:") {
		t.Errorf("missing Other settings: section in:\n%s", got)
	}
}

// Production-us hunt #9: `gregale deploy --dry-run` printed a
// deployment.workflows addition with %v — pointer addresses and the step
// input as a byte array. Composite values render as compact JSON.
func TestRenderText_CompositeValueRendersAsJSON(t *testing.T) {
	type step struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input,omitempty"`
	}
	type workflow struct {
		Name  string `json:"name"`
		Steps []step `json:"steps"`
		Retry *int   `json:"retry,omitempty"`
	}
	retries := 2
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "deployment.workflows", Kind: ChangeAdd, After: asAnyLit([]workflow{{
				Name: "h9_flow", Retry: &retries,
				Steps: []step{{Name: "second", Input: json.RawMessage(`{"order":"{{input.order}}"}`)}},
			}})},
		},
	}
	got := renderTo(t, d)
	want := `deployment.workflows + [{"name":"h9_flow","steps":[{"name":"second","input":{"order":"{{input.order}}"}}],"retry":2}]`
	if !strings.Contains(got, want) {
		t.Fatalf("composite value not rendered as JSON:\n%s\nwant substring:\n%s", got, want)
	}
	if strings.Contains(got, "0x") || strings.Contains(got, "[123 34") {
		t.Fatalf("composite value leaked Go internals:\n%s", got)
	}
}

// --- Final gate lines (Fix before deploy / Ready to deploy / No changes)

func TestRenderText_FixBeforeDeploy_Line(t *testing.T) {
	// HasBlockingBreaks → final line "Fix before deploy."
	d := Diff{
		Slug: "api",
		Breaks: []Break{
			{Severity: SeverityError, Code: "ram_cap", Reason: "too much"},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Fix before deploy.") {
		t.Errorf("missing 'Fix before deploy.' line in:\n%s", got)
	}
	if !strings.Contains(got, "Plan-quota breaks (gate fires):") {
		t.Errorf("missing breaks section in:\n%s", got)
	}
}

func TestRenderText_ReadyToDeploy_Line(t *testing.T) {
	// Non-empty Changes, no blocking breaks → "Ready to deploy."
	d := Diff{
		Slug: "api",
		Changes: []Change{
			{Field: "memory", Kind: ChangeModify, Before: asAnyLit(256), After: asAnyLit(512)},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Ready to deploy.") {
		t.Errorf("missing 'Ready to deploy.' line in:\n%s", got)
	}
}

func TestRenderText_NoChanges_Line(t *testing.T) {
	// Empty Changes + no Breaks → "No changes."
	d := Diff{Slug: "api"}
	got := renderTo(t, d)
	if !strings.Contains(got, "No changes.") {
		t.Errorf("missing 'No changes.' line in:\n%s", got)
	}
}

// --- Section break (severity split) ---------------------------------

func TestRenderText_BreaksErrorsAndWarnings(t *testing.T) {
	// splitBreaksBySeverity at render_text.go:271-283 must put
	// errors before warnings, each sorted by code ASC.
	d := Diff{
		Slug: "api",
		Breaks: []Break{
			{Severity: SeverityWarn, Code: "warn_b", Reason: "w2"},
			{Severity: SeverityError, Code: "err_z", Reason: "e2"},
			{Severity: SeverityWarn, Code: "warn_a", Reason: "w1"},
			{Severity: SeverityError, Code: "err_a", Reason: "e1"},
		},
	}
	got := renderTo(t, d)
	if !strings.Contains(got, "Plan-quota breaks (gate fires):") {
		t.Errorf("missing errors section in:\n%s", got)
	}
	if !strings.Contains(got, "Warnings:") {
		t.Errorf("missing warnings section in:\n%s", got)
	}
	// err_a must precede err_z; warn_a must precede warn_b.
	iEA := strings.Index(got, "err_a")
	iEZ := strings.Index(got, "err_z")
	if iEA < 0 || iEZ < 0 || iEA > iEZ {
		t.Errorf("errors not sorted ASC by code; got positions EA=%d EZ=%d in:\n%s", iEA, iEZ, got)
	}
	iWA := strings.Index(got, "warn_a")
	iWB := strings.Index(got, "warn_b")
	if iWA < 0 || iWB < 0 || iWA > iWB {
		t.Errorf("warnings not sorted ASC by code; got positions WA=%d WB=%d", iWA, iWB)
	}
}

// --- internal helpers ------------------------------------------------

func TestIsHeadlineScalar(t *testing.T) {
	cases := []struct {
		field string
		want  bool
	}{
		{"memory", true},
		{"concurrency", true},
		{"idle_timeout_s", true},
		{"streaming_enabled", true},
		{"websocket_enabled", true},
		{"require_authn", true},
		{"warm_snapshot_enabled", true},
		{"require_signed", false},
		{"autoscale_target_rps", false},
		{"environment.FOO", false},
		{"cron[x]", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isHeadlineScalar(c.field); got != c.want {
			t.Errorf("isHeadlineScalar(%q) = %v, want %v", c.field, got, c.want)
		}
	}
}

func TestOtherScalars_Exclusions(t *testing.T) {
	changes := []Change{
		{Field: "memory"},               // headline, exclude
		{Field: "autoscale_target_rps"}, // non-headline, keep
		{Field: "environment.X"},        // env prefix, exclude
		{Field: "cron[a]"},              // cron prefix, exclude
		{Field: "edge_rule[r]"},         // edge_rule prefix, exclude
		{Field: "require_signed"},       // non-headline, keep
	}
	got := otherScalars(changes)
	if len(got) != 2 {
		t.Fatalf("otherScalars len = %d, want 2 (%v)", len(got), got)
	}
	fields := map[string]bool{}
	for _, c := range got {
		fields[c.Field] = true
	}
	if !fields["autoscale_target_rps"] || !fields["require_signed"] {
		t.Errorf("missing expected fields in:\n%v", got)
	}
}

func TestPrintScalar_AddRemoveModify(t *testing.T) {
	cases := []struct {
		kind   ChangeKind
		want   string
		before any
		after  any
	}{
		{ChangeAdd, "+ 256", nil, 256},
		{ChangeRemove, "- 256", 256, nil},
		{ChangeModify, "256 → 512", 256, 512},
	}
	for _, c := range cases {
		ch := Change{Field: "memory", Kind: c.kind}
		if c.before != nil {
			ch.Before = AsAny(c.before)
		}
		if c.after != nil {
			ch.After = AsAny(c.after)
		}
		var buf bytes.Buffer
		printScalar(&buf, ch)
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("printScalar(%s) = %q, want substring %q", c.kind, buf.String(), c.want)
		}
		// Verify the field label is padded to 14 chars (render_text.go:208-210).
		label := "memory"
		for len(label) < 14 {
			label += " "
		}
		if !strings.Contains(buf.String(), label) {
			t.Errorf("printScalar padding missing: %q (want substring %q)", buf.String(), label)
		}
	}
}

func TestPrintBreak_ErrorAndWarningMarkers(t *testing.T) {
	// Error vs Warn marker at render_text.go:225-227.
	cases := []struct {
		sev   string
		code  string
		want  string
		field string
	}{
		{SeverityError, "err_x", "✗ err_x", ""},
		{SeverityWarn, "warn_y", "⚠ warn_y", ""},
		{SeverityError, "err_z", "(memory)", "memory"},
	}
	for _, c := range cases {
		b := Break{Severity: c.sev, Code: c.code, Reason: "x"}
		if c.field != "" {
			b.Field = c.field
		}
		var buf bytes.Buffer
		printBreak(&buf, b)
		got := buf.String()
		if !strings.Contains(got, c.want) {
			t.Errorf("printBreak(sev=%s) = %q, want substring %q", c.sev, got, c.want)
		}
	}
}

func TestFilterChanges_StableSort(t *testing.T) {
	// Stable order at render_text.go:246-251: sort by Field ASC, then
	// by Kind ASC (add, modify, remove).
	changes := []Change{
		{Field: "cron[b]", Kind: ChangeRemove},
		{Field: "cron[a]", Kind: ChangeModify},
		{Field: "cron[a]", Kind: ChangeAdd},
		{Field: "cron[a]", Kind: ChangeRemove},
		{Field: "cron[c]", Kind: ChangeAdd},
	}
	got := filterChanges(changes, "cron[")
	want := []string{
		"cron[a] add",
		"cron[a] modify",
		"cron[a] remove",
		"cron[b] remove",
		"cron[c] add",
	}
	if len(got) != len(want) {
		t.Fatalf("filterChanges len = %d, want %d", len(got), len(want))
	}
	for i, g := range got {
		suffix := ""
		switch g.Kind {
		case ChangeAdd:
			suffix = "add"
		case ChangeModify:
			suffix = "modify"
		case ChangeRemove:
			suffix = "remove"
		}
		got := g.Field + " " + suffix
		if got != want[i] {
			t.Errorf("filterChanges[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestKindOrder(t *testing.T) {
	// kindOrder at render_text.go:257-267: add < modify < remove;
	// anything else = 9.
	if kindOrder(ChangeAdd) >= kindOrder(ChangeModify) {
		t.Error("add should be < modify")
	}
	if kindOrder(ChangeModify) >= kindOrder(ChangeRemove) {
		t.Error("modify should be < remove")
	}
	if kindOrder(ChangeKind("unknown_kind")) != 9 {
		t.Error("unknown kind should map to 9")
	}
}

func TestSplitBreaksBySeverity(t *testing.T) {
	breaks := []Break{
		{Severity: SeverityWarn, Code: "w_z"},
		{Severity: SeverityError, Code: "e_y"},
		{Severity: SeverityError, Code: "e_a"},
		{Severity: SeverityWarn, Code: "w_a"},
	}
	errs, warns := splitBreaksBySeverity(breaks)
	if len(errs) != 2 {
		t.Fatalf("errs len = %d, want 2", len(errs))
	}
	if len(warns) != 2 {
		t.Fatalf("warns len = %d, want 2", len(warns))
	}
	if errs[0].Code != "e_a" || errs[1].Code != "e_y" {
		t.Errorf("errs not sorted ASC by code: %v", []string{errs[0].Code, errs[1].Code})
	}
	if warns[0].Code != "w_a" || warns[1].Code != "w_z" {
		t.Errorf("warns not sorted ASC by code: %v", []string{warns[0].Code, warns[1].Code})
	}
}
