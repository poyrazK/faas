package main

import (
	"strings"
	"testing"
)

const guestBase = `package api

import (
	"encoding/json"
	"time"
)

type Mode string

type Manifest struct {
	SchemaVersion int               ` + "`json:\"schema_version\"`" + `
	Entrypoint    []string          ` + "`json:\"entrypoint\"`" + `
	Env           map[string]string ` + "`json:\"env,omitempty\"`" + `
	Mode          Mode              ` + "`json:\"mode,omitempty\"`" + `
	Health        *Health           ` + "`json:\"health,omitempty\"`" + `
	Ports         []Port            ` + "`json:\"ports,omitempty\"`" + `
	Grace         time.Duration     ` + "`json:\"grace,omitempty\"`" + `
	Probe         json.RawMessage   ` + "`json:\"probe,omitempty\"`" + `
	Favicon       []byte            ` + "`json:\"favicon,omitempty\"`" + `
	internal      string
	Ignored       string            ` + "`json:\"-\"`" + `
	Common
}

type Common struct {
	Region string ` + "`json:\"region,omitempty\"`" + `
}

type Health struct {
	Path string ` + "`json:\"path\"`" + `
	Next *Health ` + "`json:\"next,omitempty\"`" + `
}

type Port struct {
	Number int ` + "`json:\"number\"`" + `
}
`

func guestKeys(t *testing.T, oldSrc, newSrc string) []string {
	t.Helper()
	old, err := describeGuest(map[string][]byte{"pkg/api/m.go": []byte(oldSrc)}, []string{"Manifest"})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := describeGuest(map[string][]byte{"pkg/api/m.go": []byte(newSrc)}, []string{"Manifest"})
	if err != nil {
		t.Fatal(err)
	}
	return keys(compareGuest(old, cur))
}

func TestDescribeGuest(t *testing.T) {
	s, err := describeGuest(map[string][]byte{"pkg/api/m.go": []byte(guestBase)}, []string{"Manifest"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]guestField{
		"Manifest":                {Shape: "object"},
		"Manifest.schema_version": {Shape: "integer"},
		"Manifest.entrypoint":     {Shape: "array"},
		"Manifest.entrypoint[]":   {Shape: "string"},
		"Manifest.env":            {Shape: "map", OmitEmpty: true},
		"Manifest.env{}":          {Shape: "string"},
		"Manifest.mode":           {Shape: "string", OmitEmpty: true},
		"Manifest.health":         {Shape: "object", OmitEmpty: true},
		"Manifest.health.path":    {Shape: "string"},
		"Manifest.health.next":    {Shape: "recursive:Health", OmitEmpty: true},
		"Manifest.ports":          {Shape: "array", OmitEmpty: true},
		"Manifest.ports[]":        {Shape: "object"},
		"Manifest.ports[].number": {Shape: "integer"},
		"Manifest.grace":          {Shape: "integer", OmitEmpty: true},
		"Manifest.probe":          {Shape: "any", OmitEmpty: true},
		"Manifest.favicon":        {Shape: "bytes", OmitEmpty: true},
		"Manifest.region":         {Shape: "string", OmitEmpty: true}, // promoted from Common
	}
	if len(s) != len(want) {
		t.Errorf("got %d keys, want %d: %v", len(s), len(want), s)
	}
	for p, w := range want {
		if s[p] != w {
			t.Errorf("%s = %+v, want %+v", p, s[p], w)
		}
	}
}

func TestCompareGuest(t *testing.T) {
	tests := []struct {
		name    string
		old     string // defaults to guestBase
		replace [][2]string
		want    []string
	}{
		{name: "unchanged"},
		{
			name:    "new omitempty key is compatible",
			replace: [][2]string{{"internal      string", "Restart string `json:\"restart,omitempty\"`\n\tinternal string"}},
		},
		{
			name:    "new omitzero key is compatible",
			replace: [][2]string{{"internal      string", "Restart string `json:\"restart,omitzero\"`\n\tinternal string"}},
		},
		{
			name:    "new key without omitempty",
			replace: [][2]string{{"internal      string", "Restart string `json:\"restart\"`\n\tinternal string"}},
			want:    []string{"guest:Manifest.restart:not-omitempty"},
		},
		{
			name:    "new key in a slice element without omitempty",
			replace: [][2]string{{"Number int ", "Proto string `json:\"proto\"`\n\tNumber int "}},
			want:    []string{"guest:Manifest.ports[].proto:not-omitempty"},
		},
		{
			name: "keys inside a new omitempty object need no omitempty of their own",
			replace: [][2]string{
				{"internal      string", "Limits *Limits `json:\"limits,omitempty\"`\n\tinternal string"},
				{"type Port struct", "type Limits struct {\n\tCPU int `json:\"cpu\"`\n}\n\ntype Port struct"},
			},
		},
		{
			name:    "key removed",
			replace: [][2]string{{"Grace         time.Duration     `json:\"grace,omitempty\"`", ""}},
			want:    []string{"guest:Manifest.grace:removed"},
		},
		{
			name:    "key renamed by its tag",
			replace: [][2]string{{"json:\"grace,omitempty\"", "json:\"grace_period,omitempty\""}},
			want:    []string{"guest:Manifest.grace:removed"},
		},
		{
			name:    "object removed is reported once",
			replace: [][2]string{{"Health        *Health           `json:\"health,omitempty\"`", ""}},
			want:    []string{"guest:Manifest.health:removed"},
		},
		{
			name:    "type changed",
			replace: [][2]string{{"SchemaVersion int ", "SchemaVersion string "}},
			want:    []string{"guest:Manifest.schema_version:type-changed"},
		},
		{
			name:    "named type's underlying type changed",
			replace: [][2]string{{"type Mode string", "type Mode int"}},
			want:    []string{"guest:Manifest.mode:type-changed"},
		},
		{
			name:    "pointer versus value and integer width are compatible",
			replace: [][2]string{{"Health        *Health", "Health        Health"}, {"Number int ", "Number int64 "}},
		},
		{
			name:    "Go field and type renames that keep the JSON are compatible",
			replace: [][2]string{{"Grace         time.Duration", "GracePeriod   time.Duration"}, {"type Port struct", "type WorkloadPort struct"}, {"[]Port", "[]WorkloadPort"}},
		},
		{
			name:    "gaining a custom marshaler changes the shape",
			replace: [][2]string{{"type Mode string", "type Mode string\n\nfunc (m Mode) MarshalJSON() ([]byte, error) { return nil, nil }"}},
			want:    []string{"guest:Manifest.mode:type-changed"},
		},
		{
			name:    "un-embedding a promoted struct moves its keys",
			replace: [][2]string{{"\tCommon\n", "\tCommon Common `json:\"common,omitempty\"`\n"}},
			want:    []string{"guest:Manifest.region:removed"},
		},
		{
			name:    "the string option changes the shape",
			replace: [][2]string{{"json:\"schema_version\"", "json:\"schema_version,string\""}},
			want:    []string{"guest:Manifest.schema_version:type-changed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := guestBase
			if tt.old != "" {
				old = tt.old
			}
			cur := old
			for _, r := range tt.replace {
				if !strings.Contains(cur, r[0]) {
					t.Fatalf("fixture does not contain %q", r[0])
				}
				cur = strings.Replace(cur, r[0], r[1], 1)
			}
			assertKeys(t, guestKeys(t, old, cur), tt.want)
		})
	}
}

func TestDescribeGuestMissingRoot(t *testing.T) {
	if _, err := describeGuest(map[string][]byte{"pkg/api/m.go": []byte(guestBase)}, []string{"Nope"}); err == nil {
		t.Fatal("want an error for a missing root type")
	}
}
