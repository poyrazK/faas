package api

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestMCPReadinessIncidentCursor(t *testing.T) {
	at := time.Now().UTC()
	id := uuid.NewString()
	cursor := MCPReadinessIncidentCursor(at, id)
	got, key, err := ParseMCPReadinessIncidentCursor(cursor)
	if err != nil || !got.Equal(at) || key != id {
		t.Fatal(got, key, err)
	}
	for _, bad := range []string{"bad", "?", MCPReadinessIncidentCursor(at, "bad-id")} {
		if _, _, err = ParseMCPReadinessIncidentCursor(bad); err == nil {
			t.Fatal("invalid cursor admitted", bad)
		}
	}
}
