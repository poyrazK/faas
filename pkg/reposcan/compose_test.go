package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestDetectCompose_ExtractsServices covers the canonical
// 5-service fixture: api + worker + web (build: contexts) plus
// db + cache (image: that hit the denylist).
func TestDetectCompose_ExtractsServices(t *testing.T) {
	t.Parallel()
	body := `version: "3.9"
services:
  api:
    build:
      context: ./services/api
      dockerfile: Dockerfile.api
    command: ["bundle", "exec", "rails", "s"]
    ports:
      - "8080:80"
    environment:
      RAILS_ENV: production
      DATABASE_URL: postgres://x
  worker:
    build: ./services/worker
    command: bundle exec sidekiq
    environment:
      - REDIS_URL
      - LOG_LEVEL
  web:
    build: ./apps/web
    environment:
      KEY: v
  db:
    image: postgres:15-alpine
  cache:
    image: redis:7
`
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, managed, warnings, err := detectCompose(fsys)
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	if got := names(seeds); !equalSet(got, []string{"api", "worker", "web"}) {
		t.Errorf("seed names = %v, want {api,worker,web}", got)
	}
	if got := sortManagedNames(managed); !equalSet(got, []string{"db", "cache"}) {
		t.Errorf("managed names = %v, want {db,cache}", got)
	}

	// Per-service assertions.
	for _, s := range seeds {
		switch s.name {
		case "api":
			if s.dockerfile != "Dockerfile.api" {
				t.Errorf("api dockerfile = %q, want Dockerfile.api", s.dockerfile)
			}
			if s.rootDir != "services/api" {
				t.Errorf("api rootDir = %q, want services/api", s.rootDir)
			}
			if len(s.command) != 4 || s.command[0] != "bundle" {
				t.Errorf("api command = %v, want [bundle exec rails s]", s.command)
			}
			if len(s.ports) != 1 || s.ports[0] != 8080 {
				t.Errorf("api ports = %v, want [8080]", s.ports)
			}
			if !equalSet(s.envKeys, []string{"RAILS_ENV", "DATABASE_URL"}) {
				t.Errorf("api envKeys = %v, want {RAILS_ENV,DATABASE_URL}", s.envKeys)
			}
		case "worker":
			if s.rootDir != "services/worker" {
				t.Errorf("worker rootDir = %q", s.rootDir)
			}
			if !equalSet(s.envKeys, []string{"REDIS_URL", "LOG_LEVEL"}) {
				t.Errorf("worker envKeys = %v", s.envKeys)
			}
		case "web":
			if s.rootDir != "apps/web" {
				t.Errorf("web rootDir = %q", s.rootDir)
			}
		}
	}
	for _, m := range managed {
		switch m.Name {
		case "db":
			if m.Kind != "postgres" {
				t.Errorf("db Kind = %q", m.Kind)
			}
			if m.EnvHint != "DATABASE_URL" {
				t.Errorf("db EnvHint = %q", m.EnvHint)
			}
		case "cache":
			if m.Kind != "redis" {
				t.Errorf("cache Kind = %q", m.Kind)
			}
			if m.EnvHint != "REDIS_URL" {
				t.Errorf("cache EnvHint = %q", m.EnvHint)
			}
		}
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none (all denylisted images)", warnings)
	}
}

// TestDetectCompose_SkipsPrebuiltWithoutBuild pins the tripwire
// path: a service that declares image: but no build: AND is NOT
// in the datastore denylist MUST emit a warning and NOT a workload.
// The two-drive FROM-base constraint (ADR-040) rejects arbitrary
// prebuilt base images; we surface that at discovery time.
func TestDetectCompose_SkipsPrebuiltWithoutBuild(t *testing.T) {
	t.Parallel()
	body := `services:
  web:
    image: nginx:1.25
`
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, managed, warnings, err := detectCompose(fsys)
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want none", names(seeds))
	}
	if len(managed) != 0 {
		t.Errorf("managed = %v, want none", sortManagedNames(managed))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", warnings)
	}
	if !strings.Contains(warnings[0], "nginx") || !strings.Contains(warnings[0], "refusing arbitrary prebuilt") {
		t.Errorf("warning text %q lacks 'nginx' or 'refusing arbitrary prebuilt'", warnings[0])
	}
}

func TestDetectCompose_ExtractsDependsOn(t *testing.T) {
	t.Parallel()
	body := `services:
  api:
    build: ./api
    depends_on:
      db:
        condition: service_healthy
      cache: {}
  worker:
    build: ./worker
    depends_on: [db, api, db]
  db:
    build: ./db
`
	seeds, _, _, err := detectCompose(fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(body)},
	})
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	got := make(map[string][]string, len(seeds))
	for _, seed := range seeds {
		got[seed.name] = seed.dependsOn
	}
	if !equalSet(got["api"], []string{"cache", "db"}) {
		t.Fatalf("api depends_on = %v", got["api"])
	}
	if !equalSet(got["worker"], []string{"api", "db"}) {
		t.Fatalf("worker depends_on = %v", got["worker"])
	}
}

// TestDetectCompose_PrefersComposeYAML confirms the file-pick order:
// compose.yaml > compose.yml > docker-compose.yml > docker-compose.yaml.
func TestDetectCompose_PrefersComposeYAML(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  a:
    build: .
`)},
		"docker-compose.yml": &fstest.MapFile{Data: []byte(`services:
  b:
    build: .
`)},
	}
	seeds, _, _, err := detectCompose(fsys)
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	if len(seeds) != 1 || seeds[0].name != "a" {
		t.Errorf("seeds = %v, want [{a …}] (compose.yaml wins over docker-compose.yml)", names(seeds))
	}
}

// TestDetectCompose_AbsentFile — quiet skip when no compose file is
// in the tarball. This is the common case (a tarball with only a
// Dockerfile; the Tier-4 root-floor fires later).
func TestDetectCompose_AbsentFile(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
	}
	seeds, managed, warnings, err := detectCompose(fsys)
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	if len(seeds) != 0 || len(managed) != 0 || len(warnings) != 0 {
		t.Errorf("detectCompose = (%v, %v, %v), all want empty",
			names(seeds), sortManagedNames(managed), warnings)
	}
}

// TestDetectCompose_InvalidYAMLSoftFails — bad YAML emits a warning
// instead of an error so a partially-broken compose file does not
// invalidate the whole scan.
func TestDetectCompose_InvalidYAMLSoftFails(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte("services:\n  api: {build: .")},
	}
	seeds, _, warnings, err := detectCompose(fsys)
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want empty (broken compose)", names(seeds))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "parse compose.yaml") {
		t.Errorf("warnings = %v, want [parse compose.yaml …]", warnings)
	}
}

// TestDetectCompose_BuildContextDotPrefixStripped — a build.context
// of "./services/api" normalizes to "services/api" so the merge
// key (RootDir, Name) compares equal to a workspace-detected
// "services/api" path.
func TestDetectCompose_BuildContextDotPrefixStripped(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte("services:\n  api:\n    build: {context: ./api, dockerfile: Dockerfile}\n")},
	}
	seeds, _, _, err := detectCompose(fsys)
	if err != nil {
		t.Fatalf("detectCompose: %v", err)
	}
	if len(seeds) != 1 || seeds[0].rootDir != "api" {
		t.Errorf("rootDir = %q, want api", seeds[0].rootDir)
	}
}

// helpers
func names(seeds []workloadSeed) []string {
	out := make([]string, len(seeds))
	for i, s := range seeds {
		out[i] = s.name
	}
	return out
}

func sortManagedNames(ms []Managed) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sortStrings(aa)
	sortStrings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
