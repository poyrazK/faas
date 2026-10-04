// adr: 567 — environment intent and runtime ownership contracts.
package sched

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestQueuePollerRequiresAuthoritativeBindingIdentity(t *testing.T) {
	bindingID := uuid.New()
	for _, tc := range []struct {
		name, config    string
		owned, rejected bool
	}{
		{"legacy", `{"mode":"queue"}`, false, false},
		{"unowned marker", `{"mode":"queue","queue_binding_id":"` + bindingID.String() + `"}`, false, true},
		{"null marker", `{"mode":"queue","queue_binding_id":null}`, false, true},
		{"owned projection", `{"mode":"queue","queue_binding_id":"` + bindingID.String() + `"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trigger := sqlc.Trigger{Kind: "queue", Source: pgtype.Text{String: "queue", Valid: true}, Config: []byte(tc.config)}
			if tc.owned {
				trigger.QueueBindingID = pgtype.UUID{Bytes: bindingID, Valid: true}
			}
			poller, err := newQueuePoller(nil, trigger, nil)
			if (err != nil) != tc.rejected {
				t.Fatalf("poller err=%v, rejected=%v", err, tc.rejected)
			}
			if poller != nil {
				_ = poller.Close()
			}
		})
	}
}
