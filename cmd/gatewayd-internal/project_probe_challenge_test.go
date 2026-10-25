package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

type projectProbeInvalidator struct {
	fakeInvalidator
	scope          string
	scoped, legacy int
}

func (f *projectProbeInvalidator) AuthorizeRouteProbe(string, string, string, time.Time) { f.legacy++ }
func (f *projectProbeInvalidator) AuthorizeRouteProbeForScope(_, _, _, scope string, _ time.Time) {
	f.scoped++
	f.scope = scope
}
func TestProjectProbeChallengeNotificationPreservesScope(t *testing.T) {
	for _, scope := range []string{"", "production"} {
		t.Run(scope, func(t *testing.T) {
			inv := &projectProbeInvalidator{}
			payload, err := json.Marshal(map[string]any{"app_id": "app", "deployment_id": "candidate", "token": "token", "scope": scope, "expires_at": time.Now().Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			handleInvalidation(t.Context(), inv, db.Notification{Channel: db.NotifyRouteProbeChallenge, Payload: string(payload)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if scope == "production" && (inv.scoped != 1 || inv.scope != "production" || inv.legacy != 0) {
				t.Fatalf("scoped=%d legacy=%d scope=%s", inv.scoped, inv.legacy, inv.scope)
			}
			if scope == "" && (inv.legacy != 1 || inv.scoped != 0) {
				t.Fatalf("legacy probe changed: %+v", inv)
			}
		})
	}
}
