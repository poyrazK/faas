package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func TestHostingDiscoveryRequiresLocalOptIn(t *testing.T) {
	// adr: 202610171453 — execution flags must also hide discovery surfaces.
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	for _, value := range []string{"", "false", "0", "yes", "1", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", value)
			enabled := value == "1" || value == "true"
			if strings.Contains(main.TopLevelUsage(true), "  sites ") != enabled {
				t.Fatal("advanced help does not follow local opt-in")
			}
			for _, args := range [][]string{{"help", "sites"}, {"sites", "--help"}, {"sites", "deploy", "--help"}, {"man", "sites"}} {
				var out bytes.Buffer
				(*main.OsStdout) = &out
				stderr, restore := main.CaptureStderr(t)
				code := main.Run(args)
				message := stderr.String()
				restore()
				if (code == 0) != enabled {
					t.Fatalf("%v: exit %d", args, code)
				}
				if !enabled && (out.Len() != 0 || strings.Contains(message, "FAAS_FRONTEND_HOSTING") || strings.Contains(strings.ToLower(message), "hosting")) {
					t.Fatalf("disabled help leaked: %v %s %s", args, out.String(), message)
				}
			}
			if suggestion, ok := main.SuggestCommand("sits"); !enabled && ok && suggestion == "sites" {
				t.Fatal("disabled command suggested")
			}
			if !enabled {
				stderr, restore := main.CaptureStderr(t)
				code := main.Run([]string{"sites", "list"})
				restore()
				if code == 0 || !strings.Contains(stderr.String(), "unknown command") || strings.Contains(strings.ToLower(stderr.String()), "hosting") {
					t.Fatal("disabled invocation revealed internal feature", stderr.String())
				}
			}
		})
	}
}
