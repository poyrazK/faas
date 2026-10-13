package state

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCheckpointPgEndpointCanonicalComponents(t *testing.T) {
	for _, host := range []string{"stage.example", "127.0.0.1", "2001:db8::1"} {
		if !checkpointPgEndpointHostValid(host) {
			t.Fatal("canonical host rejected", host)
		}
	}
	for _, host := range []string{"", "Stage.example", "stage.example.", "user@host", "host:5432", "[::1]", "fe80::1%eth0", "a..b", "-a.example", "a-.example", "a/b", "a?sslmode=disable", "a\\b", "a\n", "é.example", strings.Repeat("a", api.PostgresCheckpointConnectionHostMaxBytes+1)} {
		if checkpointPgEndpointHostValid(host) {
			t.Fatal("noncanonical host accepted")
		}
	}
	for _, name := range []string{"stage_data", "stage-data", "Stage42"} {
		if !checkpointPgEndpointDatabaseValid(name) {
			t.Fatal("canonical database rejected")
		}
	}
	for _, name := range []string{"", "/source", "stage/data", "stage?sslmode=disable", "stage\n", "é", strings.Repeat("a", api.PostgresCheckpointConnectionDatabaseMaxBytes+1)} {
		if checkpointPgEndpointDatabaseValid(name) {
			t.Fatal("unsafe database accepted")
		}
	}
}
