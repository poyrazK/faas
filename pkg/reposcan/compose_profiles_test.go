// adr: 20261022190000
package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestComposeProfilesFromDotEnv(t *testing.T) {
	const body = `services:
  api:
    build: ./api
  debug-console:
    profiles: [debug, tools]
    build: ./debug
  worker:
    profiles: [jobs]
    build: ./worker
`
	for _, tc := range []struct {
		name, env string
		want      []string
		skipped   []string
	}{
		{"no .env", "", []string{"api"}, []string{"debug-console", "worker"}},
		{"one profile of several", "COMPOSE_PROFILES=tools\n", []string{"api", "debug-console"}, []string{"worker"}},
		{"list with spaces", "COMPOSE_PROFILES= debug , jobs \n", []string{"api", "debug-console", "worker"}, nil},
		{"quoted value", "COMPOSE_PROFILES=\"jobs\"\n", []string{"api", "worker"}, []string{"debug-console"}},
		{"unrelated profile", "COMPOSE_PROFILES=other\n", []string{"api"}, []string{"debug-console", "worker"}},
		{"every profile", "COMPOSE_PROFILES=*\n", []string{"api", "debug-console", "worker"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(body)}}
			if tc.env != "" {
				fsys[".env"] = &fstest.MapFile{Data: []byte(tc.env)}
			}
			seeds, _, warnings, err := detectCompose(fsys)
			if err != nil {
				t.Fatal(err)
			}
			if got := names(seeds); !equalSet(got, tc.want) {
				t.Fatalf("seeds = %v, want %v", got, tc.want)
			}
			for _, service := range tc.skipped {
				found := false
				for _, warning := range warnings {
					if strings.Contains(warning, service+" skipped because profiles") && strings.Contains(warning, "COMPOSE_PROFILES") {
						found = true
					}
				}
				if !found {
					t.Fatalf("warnings %q do not explain skipping %s", warnings, service)
				}
			}
			if len(warnings) != len(tc.skipped) {
				t.Fatalf("warnings = %q, want one per skipped service %v", warnings, tc.skipped)
			}
		})
	}
}
