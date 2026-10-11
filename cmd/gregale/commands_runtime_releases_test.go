package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestDeploymentRuntimeCLIReadAndPreview(t *testing.T) {
	const id = "12345678123412341234123456789abc"
	for _, preview := range []bool{false, true} {
		t.Run(map[bool]string{false: "identity", true: "preview"}[preview], func(t *testing.T) {
			resetJSONOut(t)
			var out bytes.Buffer
			before := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = before })
			body := `{"status":"unknown","reason":"Exact identity is unknown.","releases":[]}`
			args := []string{id}
			if preview {
				body = `{"disposition":"review_required","rebuild_required":true,"cold_start_required":true,"target":{"id":"candidate"},"blockers":["Native qualification required"],"required_steps":["Rebuild the same source"]}`
				args = append(args, "--target", strings.Repeat("a", 64))
			}
			f := authedFakeAPI(t, body, http.StatusOK)
			if code := cmdDeployment(append([]string{"runtime"}, args...)); code != 0 {
				t.Fatal("exit", code)
			}
			path := "/v1/deployments/" + id + "/runtime"
			if preview {
				path += "/upgrade-preview"
			}
			if f.sawMethod != "GET" || f.sawPath != path {
				t.Fatal(f.sawMethod, f.sawPath)
			}
			expected := "Exact identity is unknown."
			if preview {
				expected = "Native qualification required"
			}
			if !strings.Contains(out.String(), expected) {
				t.Fatal(out.String())
			}
		})
	}
}
