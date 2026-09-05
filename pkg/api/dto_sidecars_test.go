package api

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testSidecarLimits returns a Limits struct with a generous
// EnvValueMaxBytes for the dto-sidecar unit tests. Production
// limits are smaller (4 KB on Hobby; 32 KB on Pro+); the test
// only needs the field to be non-zero so the per-value byte cap
// path runs. A 1 MiB cap is comfortably above every test payload.
func testSidecarLimits() Limits {
	return Limits{
		Plan:             PlanHobby,
		RAMMB:            256,
		EnvValueMaxBytes: 1 << 20,
	}
}

func TestSidecar_Validate_Accepts(t *testing.T) {
	limits := testSidecarLimits()
	essTrue := true
	cases := []struct {
		name string
		s    Sidecar
	}{
		{
			name: "init-only",
			s: Sidecar{
				Name:      "migrator",
				Image:     "ghcr.io/me/migrator@sha256:0000000000000000000000000000000000000000000000000000000000000001",
				Type:      SidecarTypeInit,
				Cmd:       []string{"--to", "head"},
				Env:       map[string]string{"DB_URL": "postgres://x"},
				Port:      0,
				RamMB:     64,
				Essential: &essTrue,
			},
		},
		{
			name: "sidecar-only",
			s: Sidecar{
				Name:  "scraper",
				Image: "ghcr.io/me/scraper@sha256:0000000000000000000000000000000000000000000000000000000000000002",
				Type:  SidecarTypeSidecar,
				Port:  9090,
				RamMB: 32,
			},
		},
		{
			name: "minimal-port-ram-absent",
			s: Sidecar{
				Name:  "only",
				Image: "r/x@sha256:" + strings.Repeat("a", 64),
				Type:  SidecarTypeInit,
			},
		},
		{
			name: "with-underscore-and-dot-image",
			s: Sidecar{
				Name:  "complex",
				Image: "registry.example.com:5000/path/to/app_v1.2@sha256:" + strings.Repeat("f", 64),
				Type:  SidecarTypeSidecar,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if p := tc.s.Validate(limits); p != nil {
				t.Errorf("Validate(Accepts) = %v, want nil", p)
			}
		})
	}
}

func TestSidecars_Validate_Dependencies(t *testing.T) {
	limits := testSidecarLimits()
	image := "ghcr.io/me/x@sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name string
		ss   Sidecars
		want string
	}{
		{
			name: "valid-main-and-init",
			ss:   Sidecars{{Name: "migrate", Image: image, Type: SidecarTypeInit}, {Name: "metrics", Image: image, Type: SidecarTypeSidecar, DependsOn: []WorkloadDependency{{Name: "migrate", Condition: WorkloadDependencyCompletedSuccessfully}}}},
		},
		{
			name: "unknown",
			ss:   Sidecars{{Name: "metrics", Image: image, Type: SidecarTypeSidecar, DependsOn: []WorkloadDependency{{Name: "missing"}}}},
			want: "unknown workload",
		},
		{
			name: "cycle-through-init-compatibility-edge",
			ss:   Sidecars{{Name: "migrate", Image: image, Type: SidecarTypeInit, DependsOn: []WorkloadDependency{{Name: "metrics"}}}, {Name: "metrics", Image: image, Type: SidecarTypeSidecar, DependsOn: []WorkloadDependency{{Name: "migrate"}}}},
			want: "cycle",
		},
		{
			name: "invalid-condition",
			ss:   Sidecars{{Name: "metrics", Image: image, Type: SidecarTypeSidecar, DependsOn: []WorkloadDependency{{Name: "main", Condition: "ready"}}}},
			want: "condition",
		},
		{
			name: "dependency-cap",
			ss: Sidecars{{Name: "metrics", Image: image, Type: SidecarTypeSidecar, DependsOn: []WorkloadDependency{
				{Name: "main"}, {Name: "a"}, {Name: "b"}, {Name: "c"},
			}}},
			want: "max is",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.ss.Validate(limits)
			if tc.want == "" {
				if p != nil {
					t.Fatalf("Validate() = %v, want nil", p)
				}
				return
			}
			if p == nil || !strings.Contains(strings.ToLower(p.Title+" "+p.Detail), tc.want) {
				t.Fatalf("Validate() = %v, want detail containing %q", p, tc.want)
			}
		})
	}
}

func TestSidecar_Validate_Rejects(t *testing.T) {
	limits := testSidecarLimits()
	essTrue := true
	essFalse := false
	goodImage := "ghcr.io/me/x@sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name    string
		s       Sidecar
		wantSub string // substring expected in the problem title or detail
	}{
		{
			name:    "name-uppercase",
			s:       Sidecar{Name: "Migrator", Image: goodImage, Type: SidecarTypeInit},
			wantSub: "sidecar name",
		},
		{
			name:    "name-empty",
			s:       Sidecar{Name: "", Image: goodImage, Type: SidecarTypeInit},
			wantSub: "sidecar name",
		},
		{
			name:    "name-leading-dash",
			s:       Sidecar{Name: "-migrator", Image: goodImage, Type: SidecarTypeInit},
			wantSub: "sidecar name",
		},
		{
			name:    "name-main-reserved",
			s:       Sidecar{Name: "main", Image: goodImage, Type: SidecarTypeSidecar},
			wantSub: "reserved",
		},
		{
			name:    "name-too-long",
			s:       Sidecar{Name: strings.Repeat("a", 64), Image: goodImage, Type: SidecarTypeInit},
			wantSub: "sidecar name",
		},
		{
			name:    "image-by-tag",
			s:       Sidecar{Name: "ok", Image: "ghcr.io/me/x:latest", Type: SidecarTypeInit},
			wantSub: "Invalid sidecar image",
		},
		{
			name:    "image-missing-digest",
			s:       Sidecar{Name: "ok", Image: "ghcr.io/me/x", Type: SidecarTypeInit},
			wantSub: "Invalid sidecar image",
		},
		{
			name:    "image-uppercase-hex",
			s:       Sidecar{Name: "ok", Image: "r/x@sha256:" + strings.Repeat("A", 64), Type: SidecarTypeInit},
			wantSub: "Invalid sidecar image",
		},
		{
			name:    "image-short-digest",
			s:       Sidecar{Name: "ok", Image: "r/x@sha256:" + strings.Repeat("a", 63), Type: SidecarTypeInit},
			wantSub: "Invalid sidecar image",
		},
		{
			name:    "type-bogus",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: SidecarType("init2")},
			wantSub: "Invalid sidecar type",
		},
		{
			name:    "type-empty",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: ""},
			wantSub: "Invalid sidecar type",
		},
		{
			name:    "cmd-empty-element",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeInit, Cmd: []string{"--to", ""}},
			wantSub: "every argv element",
		},
		{
			name: "env-value-too-long",
			s: Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeInit,
				Env: map[string]string{"BIG": strings.Repeat("x", 2<<20)}},
			wantSub: "value is",
		},
		{
			name:    "port-65536",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeSidecar, Port: 65536},
			wantSub: "sidecar port",
		},
		{
			name:    "port-negative",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeSidecar, Port: -1},
			wantSub: "sidecar port",
		},
		{
			name:    "ram-below-floor",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeSidecar, RamMB: 16},
			wantSub: "sidecar ram_mb",
		},
		{
			name:    "ram-above-ceiling",
			s:       Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeSidecar, RamMB: 1024},
			wantSub: "sidecar ram_mb",
		},
		// Stateful image rejection (issue #463 / ADR-068 §Decision 4).
		// The shared pkg/statefuldenylist matcher strips the digest
		// suffix + any registry hostname and probes every path
		// segment against the denylist set. These references are
		// well-formed digest-pinned refs (the sidecarImageRe
		// requires `host/repo@sha256:...`) that trip the gate.
		// The hint surfaces in the Detail field so the dashboard
		// can render actionable remediation copy.
		{
			name:    "image-stateful-postgres",
			s:       Sidecar{Name: "db", Image: "docker.io/library/postgres@sha256:" + strings.Repeat("0", 64), Type: SidecarTypeSidecar},
			wantSub: "stateful denylist",
		},
		{
			name:    "image-stateful-dockerhub-postgres",
			s:       Sidecar{Name: "db", Image: "docker.io/library/postgres@sha256:" + strings.Repeat("1", 64), Type: SidecarTypeInit},
			wantSub: "Remediation",
		},
		{
			name:    "image-stateful-redis",
			s:       Sidecar{Name: "cache", Image: "docker.io/library/redis@sha256:" + strings.Repeat("2", 64), Type: SidecarTypeSidecar},
			wantSub: "stateful denylist",
		},
		{
			name:    "image-stateful-clickhouse",
			s:       Sidecar{Name: "olap", Image: "ghcr.io/me/clickhouse@sha256:" + strings.Repeat("3", 64), Type: SidecarTypeSidecar},
			wantSub: "Remediation",
		},
		// essential true / false accepted; no error from Validate.
		{name: "essential-true-ok", s: Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeSidecar, Essential: &essTrue}, wantSub: ""},
		{name: "essential-false-ok", s: Sidecar{Name: "ok", Image: goodImage, Type: SidecarTypeSidecar, Essential: &essFalse}, wantSub: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.s.Validate(limits)
			if tc.wantSub == "" {
				if p != nil {
					t.Errorf("Validate(Rejects[%s]) = %v, want nil", tc.name, p)
				}
				return
			}
			if p == nil {
				t.Errorf("Validate(Rejects[%s]) = nil, want error containing %q", tc.name, tc.wantSub)
				return
			}
			body := p.Title + " " + p.Detail
			if !strings.Contains(body, tc.wantSub) {
				t.Errorf("Validate(Rejects[%s]) detail = %q, want substring %q", tc.name, body, tc.wantSub)
			}
		})
	}
}

func TestSidecars_Validate_Accepts(t *testing.T) {
	limits := testSidecarLimits()
	goodImage := "ghcr.io/me/x@sha256:" + strings.Repeat("a", 64)
	goodImage2 := "ghcr.io/me/y@sha256:" + strings.Repeat("b", 64)
	cases := []struct {
		name string
		ss   Sidecars
	}{
		{"empty", nil},
		{"empty-slice", Sidecars{}},
		{"init-only", Sidecars{
			{Name: "migrator", Image: goodImage, Type: SidecarTypeInit},
		}},
		{"sidecar-only", Sidecars{
			{Name: "scraper", Image: goodImage, Type: SidecarTypeSidecar},
		}},
		{"one-init-one-sidecar", Sidecars{
			{Name: "migrator", Image: goodImage, Type: SidecarTypeInit},
			{Name: "scraper", Image: goodImage2, Type: SidecarTypeSidecar},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if p := tc.ss.Validate(limits); p != nil {
				t.Errorf("Validate(Accepts[%s]) = %v, want nil", tc.name, p)
			}
		})
	}
}

func TestSidecars_Validate_Rejects(t *testing.T) {
	limits := testSidecarLimits()
	goodImage := "ghcr.io/me/x@sha256:" + strings.Repeat("a", 64)
	goodImage2 := "ghcr.io/me/y@sha256:" + strings.Repeat("b", 64)
	goodImage3 := "ghcr.io/me/z@sha256:" + strings.Repeat("c", 64)
	cases := []struct {
		name     string
		ss       Sidecars
		wantSub  string
		wantCode string // RFC 7807 stable code; "" = don't assert (substring-only rows)
	}{
		{
			// AC #3 (issue #463 / ADR-069 / PR-B): a
			// 3-element sidecars array exceeds the
			// SidecarCapMax=2 cap and the DTO gate MUST
			// surface the literal CodeSidecarCapExceeded
			// so the SDK can branch on the wire code
			// (not on prose). The closed enum is
			// pinned here so a reword in pkg/api/errors.go
			// fails this test in the same commit.
			name: "three-sidecars-over-cap",
			ss: Sidecars{
				{Name: "a", Image: goodImage, Type: SidecarTypeInit},
				{Name: "b", Image: goodImage2, Type: SidecarTypeSidecar},
				{Name: "c", Image: goodImage3, Type: SidecarTypeInit},
			},
			wantSub:  "Too many sidecars",
			wantCode: CodeSidecarCapExceeded,
		},
		{
			name: "two-init-duplicate-type",
			ss: Sidecars{
				{Name: "a", Image: goodImage, Type: SidecarTypeInit},
				{Name: "b", Image: goodImage2, Type: SidecarTypeInit},
			},
			wantSub: "at most one sidecar of type",
		},
		{
			name: "two-sidecar-duplicate-type",
			ss: Sidecars{
				{Name: "a", Image: goodImage, Type: SidecarTypeSidecar},
				{Name: "b", Image: goodImage2, Type: SidecarTypeSidecar},
			},
			wantSub: "at most one sidecar of type",
		},
		{
			name: "duplicate-name",
			ss: Sidecars{
				{Name: "dup", Image: goodImage, Type: SidecarTypeInit},
				{Name: "dup", Image: goodImage2, Type: SidecarTypeSidecar},
			},
			wantSub: "appears more than once",
		},
		{
			name: "per-element-validation-propagates",
			ss: Sidecars{
				{Name: "ok", Image: goodImage, Type: SidecarTypeInit},
				{Name: "BAD-Name", Image: goodImage2, Type: SidecarTypeSidecar},
			},
			wantSub: "sidecar name",
		},
		{
			name: "per-element-image-tag-propagates",
			ss: Sidecars{
				{Name: "ok", Image: goodImage, Type: SidecarTypeInit},
				{Name: "ok2", Image: "r/x:latest", Type: SidecarTypeSidecar},
			},
			wantSub: "Invalid sidecar image",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.ss.Validate(limits)
			if p == nil {
				t.Errorf("Validate(Rejects[%s]) = nil, want error containing %q", tc.name, tc.wantSub)
				return
			}
			body := p.Title + " " + p.Detail
			if !strings.Contains(body, tc.wantSub) {
				t.Errorf("Validate(Rejects[%s]) detail = %q, want substring %q", tc.name, body, tc.wantSub)
			}
			if tc.wantCode != "" && p.Code != tc.wantCode {
				t.Errorf("Validate(Rejects[%s]) code = %q, want %q (RFC 7807 stable code)",
					tc.name, p.Code, tc.wantCode)
			}
		})
	}
}

// TestSidecar_JSONRoundTrip pins that the wire shape is stable:
// json.Marshal + json.Unmarshal preserves every field. PR-A's
// contract is the wire shape; if any field drifts between the
// SDK gen and the on-the-wire shape, this test catches it.
//
// The Essential field is *bool (a tri-state: nil / *false / *true)
// because the customer must distinguish "I didn't set it" (nil,
// defaults to true at runtime) from "I explicitly set it false".
// A round-trip regression that flattens *bool to bool would lose
// the nil case — the customer can't tell "explicit false" from
// "absent". The three cases below pin every tri-state combination
// against the wire JSON.
func TestSidecar_JSONRoundTrip(t *testing.T) {
	essTrue := true
	essFalse := false
	image := "ghcr.io/me/x@sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name     string
		original Sidecar
	}{
		{
			name: "essential-true-all-fields-set",
			original: Sidecar{
				Name:      "migrator",
				Image:     image,
				Type:      SidecarTypeInit,
				Cmd:       []string{"--to", "head"},
				Env:       map[string]string{"DB_URL": "postgres://x"},
				Port:      9090,
				RamMB:     64,
				Essential: &essTrue,
			},
		},
		{
			name: "essential-false-explicit",
			original: Sidecar{
				Name:      "scraper",
				Image:     image,
				Type:      SidecarTypeSidecar,
				Port:      9090,
				RamMB:     32,
				Essential: &essFalse,
			},
		},
		{
			name: "essential-nil-absent",
			original: Sidecar{
				Name:  "minimal",
				Image: image,
				Type:  SidecarTypeInit,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.original)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			var got Sidecar
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("json.Unmarshal: %v (raw=%s)", err, raw)
			}
			if got.Name != tc.original.Name {
				t.Errorf("Name: got %q, want %q", got.Name, tc.original.Name)
			}
			if got.Image != tc.original.Image {
				t.Errorf("Image: got %q, want %q", got.Image, tc.original.Image)
			}
			if got.Type != tc.original.Type {
				t.Errorf("Type: got %q, want %q", got.Type, tc.original.Type)
			}
			if len(got.Cmd) != len(tc.original.Cmd) {
				t.Errorf("Cmd length: got %d, want %d", len(got.Cmd), len(tc.original.Cmd))
			}
			if got.Env["DB_URL"] != tc.original.Env["DB_URL"] {
				t.Errorf("Env[DB_URL]: got %q, want %q", got.Env["DB_URL"], tc.original.Env["DB_URL"])
			}
			if got.Port != tc.original.Port {
				t.Errorf("Port: got %d, want %d", got.Port, tc.original.Port)
			}
			if got.RamMB != tc.original.RamMB {
				t.Errorf("RamMB: got %d, want %d", got.RamMB, tc.original.RamMB)
			}
			// Essential tri-state pin: nil must round-trip as nil,
			// *true as *true, *false as *false. A nil-vs-false
			// regression (e.g. someone "simplifies" *bool to bool)
			// would lose the "I explicitly said false" signal.
			switch {
			case tc.original.Essential == nil && got.Essential != nil:
				t.Errorf("Essential: got %v, want nil (absent → absent)", *got.Essential)
			case tc.original.Essential != nil && got.Essential == nil:
				t.Errorf("Essential: got nil, want %v (explicit value lost)", *tc.original.Essential)
			case tc.original.Essential != nil && *got.Essential != *tc.original.Essential:
				t.Errorf("Essential: got %v, want %v", *got.Essential, *tc.original.Essential)
			}
		})
	}
}

// TestSidecarImage_OpenAPIRegexParity pins that the OpenAPI
// `Sidecar.image` pattern matches / rejects the SAME fixtures as the
// Go-side `sidecarImageRe`. The two live in different files (the
// yaml is the source of truth for SDK gen + vacuum; the Go regex
// is the runtime gate). Drift between them produces one of two
// failure modes:
//
//  1. yaml accepts something Go rejects → SDK/CLI requests pass
//     schema validation but the API 400s them. Customer sees a
//     misleading error message.
//  2. Go accepts something yaml rejects → SDK/CLI requests are
//     rejected pre-flight; the customer can never deploy the
//     shape the API actually supports.
//
// Both branches are easy to introduce (one is a yaml tweak, the
// other a Go regex tweak, both intentional and unflagged by lint).
// This test catches both by reading the actual yaml, compiling
// its pattern, and re-running the gate fixtures against both
// regexes. If the two diverge on any fixture, the test fails with
// which side disagrees.
//
// The fixtures mirror TestSidecar_Validate_Accepts / Rejects so a
// reader can grep one place to see the full acceptance contract.
func TestSidecarImage_OpenAPIRegexParity(t *testing.T) {
	openapiPath := findRepoOpenAPI(t)
	doc := loadOpenAPIDoc(t, openapiPath)

	// Locate the Sidecar schema. The yaml structure is
	// components.schemas.Sidecar.properties.image.pattern.
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	sidecar, ok := schemas["Sidecar"].(map[string]any)
	if !ok {
		t.Fatal("api/openapi.yaml: components.schemas.Sidecar not found")
	}
	props := sidecar["properties"].(map[string]any)
	image, ok := props["image"].(map[string]any)
	if !ok {
		t.Fatal("api/openapi.yaml: Sidecar.properties.image not found")
	}
	pattern, ok := image["pattern"].(string)
	if !ok || pattern == "" {
		t.Fatal("api/openapi.yaml: Sidecar.properties.image.pattern missing or empty")
	}
	openapiRe, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("openapi pattern does not compile as Go regex: %v (pattern=%q)", err, pattern)
	}

	cases := []struct {
		name   string
		ref    string
		wantOK bool
	}{
		// Accepts — both gates MUST match.
		{"ghcr", "ghcr.io/me/migrator@sha256:" + strings.Repeat("a", 64), true},
		{"dockerhub-with-library", "docker.io/library/postgres@sha256:" + strings.Repeat("b", 64), true},
		{"registry-port", "registry.example.com:5000/path/to/app_v1.2@sha256:" + strings.Repeat("f", 64), true},
		{"r-x-short-host", "r/x@sha256:" + strings.Repeat("c", 64), true},

		// Rejects — both gates MUST NOT match. The shapes mirror
		// TestSidecar_Validate_Rejects / TestCreateDeployment_DigestPinning_RejectsTagReference
		// so a drift here is a contract break.
		{"tag-ref", "ghcr.io/me/x:latest", false},
		{"missing-digest", "ghcr.io/me/x", false},
		{"uppercase-hex", "r/x@sha256:" + strings.Repeat("A", 64), false},
		{"short-digest", "r/x@sha256:" + strings.Repeat("a", 63), false},
		{"missing-slash-before-digest", "postgres@sha256:" + strings.Repeat("d", 64), false},
		{"tag-with-digest-mix", "ghcr.io/me/x:latest@sha256:" + strings.Repeat("e", 64), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goOK := sidecarImageRe.MatchString(tc.ref)
			openapiOK := openapiRe.MatchString(tc.ref)
			if goOK != openapiOK {
				t.Fatalf("drift on %q: Go=%v OpenAPI=%v (wantOK=%v). "+
					"Update one regex to match the other — both must agree "+
					"to keep the schema-validation / runtime-gate contract "+
					"intact (issue #463 / ADR-068).",
					tc.ref, goOK, openapiOK, tc.wantOK)
			}
			if goOK != tc.wantOK {
				t.Errorf("%s: gate returned %v, want %v", tc.ref, goOK, tc.wantOK)
			}
		})
	}
}

// findRepoOpenAPI walks up from the test's working directory until
// it finds api/openapi.yaml. The test runs from pkg/api (via `go
// test ./pkg/api/...`) so the repo root is ../.. — but to be
// robust against future layout changes, walk at most 5 levels.
func findRepoOpenAPI(t *testing.T) string {
	t.Helper()
	for _, rel := range []string{"api/openapi.yaml", "../api/openapi.yaml", "../../api/openapi.yaml"} {
		if _, err := os.Stat(rel); err == nil {
			return rel
		}
	}
	t.Fatal("could not locate api/openapi.yaml (walked up 2 levels)")
	return ""
}

// loadOpenAPIDoc parses the OpenAPI yaml into a generic map so the
// parity test can pluck the Sidecar schema's image pattern without
// dragging in a full OpenAPI library. The yaml is the source of
// truth that `make spec-sync` mirrors into pkg/apid/openapi.yaml;
// this test reads the source so a drift between the two is caught
// by spec-check before this test runs.
func loadOpenAPIDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}
