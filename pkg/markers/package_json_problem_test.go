package markers

import (
	"strings"
	"testing"
)

// Prod 2026-11-23: an invalid package.json built into an image with no app
// layer and failed later with an internal error blamed on the platform.
func TestNodePackageJSONProblemAtRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		root  string
		want  string
	}{
		{"valid", map[string]string{"package.json": `{"name":"x"}`}, "", ""},
		{"absent", map[string]string{"requirements.txt": "flask\n"}, "", ""},
		{"truncated", map[string]string{"package.json": "{\"name\": \"x\",\n\"scripts\": {\"start\": \"node s.js\"},\n"}, "", "package.json is not valid JSON: unexpected end of JSON input (line 3)"},
		{"trailing comma", map[string]string{"package.json": "{\n  \"name\": \"x\",\n}\n"}, "", "package.json is not valid JSON: invalid character '}' looking for beginning of object key string (line 3)"},
		{"array", map[string]string{"package.json": `["x"]`}, "", "package.json is not a JSON object"},
		{"workspace member", map[string]string{"package.json": `{}`, "apps/api/package.json": `{`}, "apps/api", "package.json is not valid JSON: unexpected end of JSON input (line 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tempTarball(t)
			writeTarGz(t, path, tc.files)
			got := NodePackageJSONProblemAtRoot(path, tc.root)
			if tc.want == "" && got != "" || tc.want != "" && !strings.HasPrefix(got, tc.want) {
				t.Fatalf("NodePackageJSONProblemAtRoot = %q, want prefix %q", got, tc.want)
			}
		})
	}
}
