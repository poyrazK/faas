package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// ADR-20261029120000 amendment 2: a prebuilt image a request-serving service
// depends on serves requests when its config exposes a port.
func TestComposeCalledImageServices(t *testing.T) {
	const compose = `services:
  gateway: {image: ghcr.io/x/gateway:1, ports: ["80:80"], depends_on: [api, worker]}
  api: {image: ghcr.io/x/api:1}
  worker: {image: ghcr.io/x/api:1, command: /app/worker}
`
	fsys := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(compose)}}
	for _, tc := range []struct {
		name            string
		listens, known  bool
		wantAPI         Class
		wantUnknown     bool
		wantUnreadWarns bool
	}{
		{"the image exposes a port", true, true, ClassHTTP, false, false},
		{"the image exposes nothing", false, true, ClassWorker, false, false},
		{"the registry cannot say", false, false, ClassWorker, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			lookup := func(image string) (bool, bool) {
				asked = append(asked, image)
				return tc.listens, tc.known
			}
			result, err := ScanWithOptions(fsys, ScanOptions{ImageListening: lookup})
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]Workload{}
			for _, w := range result.Workloads {
				byName[w.Name] = w
			}
			if got := byName["api"]; got.Class != tc.wantAPI || got.ListenerUnknown != tc.wantUnknown {
				t.Fatalf("api class=%s unknown=%t, want %s %t", got.Class, got.ListenerUnknown, tc.wantAPI, tc.wantUnknown)
			}
			// The worker overrides the image's command, so its image says nothing.
			if got := byName["worker"]; got.Class != ClassWorker || got.ListenerUnknown {
				t.Fatalf("worker class=%s unknown=%t, want a plain worker", got.Class, got.ListenerUnknown)
			}
			if len(asked) != 1 || asked[0] != "ghcr.io/x/api:1" {
				t.Fatalf("asked about %v, want the api image once", asked)
			}
			unread := false
			for _, warning := range result.Warnings {
				unread = unread || strings.Contains(warning, `"api"`) && strings.Contains(warning, "could not read whether its image")
			}
			if unread != tc.wantUnreadWarns {
				t.Fatalf("warnings = %v; want the unread-image note %t", result.Warnings, tc.wantUnreadWarns)
			}
		})
	}

	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range result.Workloads {
		if w.Name == "api" && (w.Class != ClassWorker || w.ListenerUnknown) {
			t.Fatalf("Scan without a lookup made api class=%s unknown=%t", w.Class, w.ListenerUnknown)
		}
	}
}
