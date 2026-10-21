package releasediff

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021101117 — what a release changed, by key, never by value.

var t0 = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func release(id string, at time.Time, edit func(*state.Deployment)) state.Deployment {
	d := state.Deployment{ID: id, AppID: "app", Scope: api.DefaultEnvScope, Status: state.DeploySuperseded, CreatedAt: at,
		CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceURL: "https://github.com/acme/shop.git", ImageDigest: "sha256:a",
		Handler: "index.handler", ReleaseCommand: []string{"node", "migrate.mjs"}}
	if edit != nil {
		edit(&d)
	}
	return d
}

func TestPredecessorIsTheLastReleaseThatServed(t *testing.T) {
	to := release("to", t0.Add(4*time.Hour), func(d *state.Deployment) { d.Status = state.DeployLive })
	rows := []state.Deployment{
		release("old", t0, nil),
		release("served", t0.Add(time.Hour), nil),
		release("failed", t0.Add(2*time.Hour), func(d *state.Deployment) { d.Status = state.DeployFailed }),
		release("staging", t0.Add(3*time.Hour), func(d *state.Deployment) { d.Scope = "staging" }),
		release("newer", t0.Add(5*time.Hour), nil),
		to,
	}
	got, ok := Predecessor(rows, to)
	if !ok || got.ID != "served" {
		t.Fatalf("predecessor = %q, %v; want served", got.ID, ok)
	}
	if _, ok := Predecessor([]state.Deployment{to}, to); ok {
		t.Fatal("a first release has no predecessor")
	}
}

func TestCompareReportsEachSection(t *testing.T) {
	from := release("from", t0, func(d *state.Deployment) {
		d.Revision = 41
		d.OverrideEnv = json.RawMessage(`{"A":"1","B":"2"}`)
		d.Sidecars = json.RawMessage(`[{"name":"proxy"}]`)
	})
	to := release("to", t0.Add(time.Hour), func(d *state.Deployment) {
		d.Revision, d.CommitSHA, d.ImageDigest, d.Handler = 42, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "sha256:b", "main.handler"
		d.ReleaseCommand = []string{"node", "migrate.mjs", "--to", "7"}
		d.OverrideEnv = json.RawMessage(`{"A":"9","C":"3"}`)
		d.Sidecars = json.RawMessage(` [ { "name" : "proxy" } ] `)
	})
	fromCfg := &state.ConfigRevision{Revision: 3, Variables: map[string]string{"KEEP": "1", "GONE": "x", "EDIT": "a"},
		Secrets: map[string]state.ConfigSecretFingerprint{"TOKEN": {Version: 1}, "OLD": {Version: 1}}}
	toCfg := &state.ConfigRevision{Revision: 5, Variables: map[string]string{"KEEP": "1", "EDIT": "b", "NEW": "y"},
		Secrets: map[string]state.ConfigSecretFingerprint{"TOKEN": {Version: 2}}}
	d := Compare(Inputs{App: state.App{Slug: "shop"}, From: &from, To: to, FromConfig: fromCfg, ToConfig: toCfg, PkgsKnown: true,
		FromPkgs: []Package{{"pkg:npm/express", "4.18.0"}, {"pkg:npm/left-pad", "1.0.0"}},
		ToPkgs:   []Package{{"pkg:npm/express", "4.19.2"}, {"pkg:npm/zod", "3.23.0"}}})

	if d.From == nil || d.From.Revision != 41 || d.To.Revision != 42 {
		t.Fatalf("sides = %+v → %+v", d.From, d.To)
	}
	if !d.Code.Changed || !d.Code.ImageChanged ||
		d.Code.CompareURL != "https://github.com/acme/shop/compare/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("code = %+v", d.Code)
	}
	wantVars := []api.ReleaseDiffKey{{Key: "EDIT", Change: "changed"}, {Key: "GONE", Change: "removed"}, {Key: "NEW", Change: "added"}}
	wantSecrets := []api.ReleaseDiffKey{{Key: "OLD", Change: "removed"}, {Key: "TOKEN", Change: "changed"}}
	if !d.Config.Known || !reflect.DeepEqual(d.Config.Variables, wantVars) || !reflect.DeepEqual(d.Config.Secrets, wantSecrets) {
		t.Fatalf("config = %+v", d.Config)
	}
	encoded, _ := json.Marshal(d)
	for _, value := range []string{`"a"`, `"b"`, `"y"`, `"9"`} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("diff leaked the value %s: %s", value, encoded)
		}
	}
	deps := d.Dependencies
	if !deps.Known || len(deps.Added) != 1 || deps.Added[0].Name != "pkg:npm/zod" || len(deps.Removed) != 1 ||
		len(deps.Changed) != 1 || deps.Changed[0].From != "4.18.0" || deps.Changed[0].To != "4.19.2" {
		t.Fatalf("dependencies = %+v", deps)
	}
	fields := map[string]api.ReleaseDiffField{}
	for _, f := range d.Runtime {
		fields[f.Field] = f
	}
	if fields["handler"].To != "main.handler" || fields["env_override_keys"].From != "A,B" || fields["env_override_keys"].To != "A,C" {
		t.Fatalf("runtime = %+v", d.Runtime)
	}
	if _, ok := fields["sidecars"]; ok {
		t.Fatalf("reformatted JSON must not count as a change: %+v", d.Runtime)
	}
	if !d.Database.Changed || d.Database.ToReleaseCommand != "node migrate.mjs --to 7" {
		t.Fatalf("database = %+v", d.Database)
	}
	if d.Summary != "new code, 5 config keys, 3 packages, 2 runtime settings, release command changed" {
		t.Fatalf("summary = %q", d.Summary)
	}
}

func TestCompareUnknownSectionsAndFirstRelease(t *testing.T) {
	from, to := release("from", t0, nil), release("to", t0.Add(time.Hour), nil)
	d := Compare(Inputs{App: state.App{Slug: "shop"}, From: &from, To: to, ConfigNote: "history pruned", PkgsNote: "image releases have no build SBOM"})
	if d.Config.Known || d.Config.Note != "history pruned" || d.Dependencies.Known || d.Dependencies.Note != "image releases have no build SBOM" ||
		d.Code.Changed || d.Summary != "no changes found" {
		t.Fatalf("diff = %+v", d)
	}
	first := Compare(Inputs{App: state.App{Slug: "shop"}, To: to})
	if first.From != nil || first.Summary != "first release" || !first.Code.Changed {
		t.Fatalf("first = %+v", first)
	}
}

func TestConfigAtPicksTheRevisionInForce(t *testing.T) {
	revs := []state.ConfigRevision{{Revision: 7, CreatedAt: t0.Add(2 * time.Hour)}, {Revision: 5, CreatedAt: t0}, {Revision: 6, CreatedAt: t0.Add(time.Hour)}}
	if r, ok := ConfigAt(revs, t0.Add(90*time.Minute)); !ok || r.Revision != 6 {
		t.Fatalf("at 1h30 = %+v, %v", r, ok)
	}
	if _, ok := ConfigAt(revs, t0.Add(-time.Minute)); ok {
		t.Fatal("no revision is older than the release")
	}
}

func TestCompareURLOnlyForTheSameGitHubRepository(t *testing.T) {
	a, b := "1111111", "2222222"
	for _, tc := range []struct {
		from, to, want string
	}{
		{"https://github.com/acme/shop", "https://github.com/acme/shop.git", "https://github.com/acme/shop/compare/1111111...2222222"},
		{"https://github.com/acme/shop", "https://github.com/acme/other", ""},
		{"https://gitlab.com/acme/shop", "https://gitlab.com/acme/shop", ""},
		{"http://github.com/acme/shop", "http://github.com/acme/shop", ""},
	} {
		if got := CompareURL(tc.from, tc.to, a, b); got != tc.want {
			t.Fatalf("CompareURL(%q, %q) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
	if CompareURL("https://github.com/acme/shop", "https://github.com/acme/shop", "main", b) != "" {
		t.Fatal("a non-SHA ref must not be linked")
	}
}

func TestParseSBOMAndPackageLimits(t *testing.T) {
	sbom := `{"bomFormat":"CycloneDX","components":[
		{"name":"express","version":"4.19.2","purl":"pkg:npm/express@4.19.2"},
		{"group":"org.slf4j","name":"slf4j-api","version":"2.0.9"},
		{"name":"scoped","version":"1.0.0","purl":"pkg:npm/%40acme/scoped@1.0.0?vcs=x"}]}`
	pkgs, err := ParseSBOM(strings.NewReader(sbom), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{{"pkg:npm/express", "4.19.2"}, {"org.slf4j/slf4j-api", "2.0.9"}, {"pkg:npm/%40acme/scoped", "1.0.0"}}
	if !reflect.DeepEqual(pkgs, want) {
		t.Fatalf("packages = %+v", pkgs)
	}
	if _, err := ParseSBOM(strings.NewReader(sbom), 10); err != ErrSBOMTooLarge {
		t.Fatalf("oversized SBOM err = %v", err)
	}
	var many []Package
	for i := 0; i < 5; i++ {
		many = append(many, Package{Name: "pkg:npm/p" + string(rune('a'+i)), Version: "1"})
	}
	added, _, _, truncated := PackageChanges(nil, many, 3)
	if len(added) != 3 || !truncated || added[0].Name != "pkg:npm/pa" {
		t.Fatalf("added = %+v, truncated = %v", added, truncated)
	}
}
