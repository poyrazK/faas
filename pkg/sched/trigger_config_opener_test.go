package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 20261110210000 slice 3g — with a trigger config opener, a broker
// poller's credentials come by reference (app, trigger), never from this
// node's identities; a trigger with no app is refused.
func TestOpenTriggerConfigByReference(t *testing.T) {
	app, trigger := uuid.New(), uuid.New()
	var asked string
	l := (&sched.Loop{}).WithTriggerConfigOpener(func(_ context.Context, appID, triggerID string) (json.RawMessage, error) {
		asked = appID + "/" + triggerID
		return json.RawMessage(`{"sasl":{"password":"opened"}}`), nil
	})
	got, err := l.OpenTriggerConfigForTest(sqlc.Trigger{ID: pgtype.UUID{Bytes: trigger, Valid: true},
		AppID: pgtype.UUID{Bytes: app, Valid: true}, Kind: "kafka", Config: []byte(`{"sasl":{"password_sealed":"x"}}`)})
	if err != nil || string(got) != `{"sasl":{"password":"opened"}}` || asked != app.String()+"/"+trigger.String() {
		t.Fatalf("opened %s, %v, asked %q", got, err, asked)
	}
	if _, err := l.OpenTriggerConfigForTest(sqlc.Trigger{ID: pgtype.UUID{Bytes: trigger, Valid: true}, Kind: "kafka"}); !errors.Is(err, sched.ErrTriggerWithoutApp) {
		t.Fatalf("trigger without an app = %v", err)
	}
}
