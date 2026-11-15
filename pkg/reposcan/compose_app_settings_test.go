package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestComposeStreamingAndWakeAheadKeys(t *testing.T) {
	fsys := fstest.MapFS{"compose.yaml": {Data: []byte(`services:
  events:
    image: ghcr.io/x/events:1
    ports: ["8080:8080"]
    x-gregale-streaming: true
    x-gregale-wake-ahead: false
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"]}
`)}}
	result, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range result.Workloads {
		switch w.Name {
		case "events":
			if w.Streaming == nil || !*w.Streaming || w.WakeAhead == nil || *w.WakeAhead {
				t.Fatalf("events streaming=%v wake-ahead=%v", w.Streaming, w.WakeAhead)
			}
		case "web":
			if w.Streaming != nil || w.WakeAhead != nil {
				t.Fatalf("undeclared settings set: streaming=%v wake-ahead=%v", w.Streaming, w.WakeAhead)
			}
		}
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "x-gregale-") {
			t.Fatalf("key reported as unsupported: %s", warning)
		}
	}
}
