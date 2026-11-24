package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParsePostgresSuspendTimeout(t *testing.T) {
	for input, want := range map[string]int64{"1m": 60, "2m": 120, "90s": 90, "1h": 3600, "default": 0} {
		got, ok := main.ParsePostgresSuspendTimeout(input)
		if !ok || (input == "default") != (got == nil) || got != nil && *got != want {
			t.Errorf("ParsePostgresSuspendTimeout(%q) = %v, %v; want %d", input, got, ok, want)
		}
	}
	for _, input := range []string{"59s", "61m", "2", "1.5s", "", "forever", "-1m"} {
		if _, ok := main.ParsePostgresSuspendTimeout(input); ok {
			t.Errorf("ParsePostgresSuspendTimeout(%q) accepted", input)
		}
	}
}

func TestRenderPostgresSuspendTimeout(t *testing.T) {
	line := main.RenderPostgresSuspendTimeout("orders-db", api.ManagedPostgresSuspendTimeout{Seconds: 300, Custom: true, Applied: true})
	if line != "orders-db: suspends after 5m0s idle; each wake bills at least that much compute" {
		t.Fatalf("applied custom = %q", line)
	}
	line = main.RenderPostgresSuspendTimeout("orders-db", api.ManagedPostgresSuspendTimeout{Seconds: 120})
	if !strings.Contains(line, "2m0s idle (platform default)") || !strings.Contains(line, "not applied yet") {
		t.Fatalf("pending default = %q", line)
	}
}
