// adr: 20261017233500
package selectedtrace

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const trace = "4bf92f3577b34da6a3ce929d0e0e4736"
const account = "11111111-1111-4111-8111-111111111111"
const appID = "22222222-2222-4222-8222-222222222222"

type lookupFixture struct {
	apps                          map[string]state.App
	reads                         int
	requestError, invocationError bool
	foreign                       bool
	change                        bool
	cancel                        context.CancelFunc
}

func (f *lookupFixture) AppByID(_ context.Context, id string) (state.App, error) {
	a, ok := f.apps[id]
	if !ok {
		return a, state.ErrNotFound
	}
	return a, nil
}
func (f *lookupFixture) ListSelectedTraceRequests(_ context.Context, q sqlc.ListSelectedTraceRequestsParams) ([]sqlc.ListSelectedTraceRequestsRow, error) {
	f.reads++
	if uuid.UUID(q.AccountID.Bytes).String() != account || len(q.AppIds) != 1 || uuid.UUID(q.AppIds[0].Bytes).String() != appID || q.TraceID != trace || q.RowLimit != 2 || !q.ReceivedFrom.Time.Before(q.ReceivedUntil.Time) || q.ReceivedUntil.Time.Nanosecond()%1000 != 0 {
		panic("unscoped or widened native request query")
	}
	if f.cancel != nil {
		f.cancel()
	}
	if f.requestError {
		return nil, errors.New("PRIVATE_REQUEST_ERROR")
	}
	id := q.AppIds[0]
	if f.foreign {
		id = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
	row := sqlc.ListSelectedTraceRequestsRow{AppID: id, ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Count: 1, ReceivedAt: q.ReceivedFrom}
	return []sqlc.ListSelectedTraceRequestsRow{row, row}, nil
}
func (f *lookupFixture) ListSelectedTraceInvocations(_ context.Context, q sqlc.ListSelectedTraceInvocationsParams) ([]sqlc.ListSelectedTraceInvocationsRow, error) {
	f.reads++
	if uuid.UUID(q.AccountID.Bytes).String() != account || len(q.AppIds) != 1 || uuid.UUID(q.AppIds[0].Bytes).String() != appID || q.TraceID != trace || q.RowLimit != 2 {
		panic("unscoped native invocation query")
	}
	if f.change {
		a := f.apps[appID]
		a.Slug = "replacement"
		f.apps[appID] = a
	}
	if f.invocationError {
		return nil, errors.New("PRIVATE_INVOCATION_ERROR")
	}
	return []sqlc.ListSelectedTraceInvocationsRow{{AppID: q.AppIds[0], ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Source: "queue", State: "pending", CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-365 * 24 * time.Hour), Valid: true}}}, nil
}

func TestSelectedTraceParse(t *testing.T) {
	for _, q := range []url.Values{{}, {"app_id": {}}, {"app_id": {appID, appID}}, {"app_id": {"00000000-0000-0000-0000-000000000000"}}, {"app_id": {appID}, "limit": {"0"}}, {"app_id": {appID}, "limit": {"1", "2"}}, {"app_id": {appID}, "scope": {"account"}}} {
		if _, p := Parse(trace, q); p == nil {
			t.Fatal("accepted invalid native selection")
		}
	}
	for _, id := range []string{"", "00000000000000000000000000000000", "4BF92F3577B34DA6A3CE929D0E0E4736"} {
		if _, p := Parse(id, url.Values{"app_id": {appID}}); p == nil {
			t.Fatal("accepted invalid trace")
		}
	}
	if s, p := Parse(trace, url.Values{"app_id": {appID}}); p != nil || s.Limit != api.SelectedTraceRowsDefault {
		t.Fatal("default native selection failed")
	}
}

func TestSelectedTraceIndependentSources(t *testing.T) {
	for _, mode := range []string{"ok", "requests_failed", "invocations_failed", "both_failed", "foreign_app", "foreign_row", "changed_app", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := &lookupFixture{apps: map[string]state.App{appID: {ID: appID, AccountID: account, Slug: "alpha"}}}
			f.requestError = mode == "requests_failed" || mode == "both_failed"
			f.invocationError = mode == "invocations_failed" || mode == "both_failed"
			f.foreign = mode == "foreign_row"
			f.change = mode == "changed_app"
			if mode == "foreign_app" {
				a := f.apps[appID]
				a.AccountID = uuid.NewString()
				f.apps[appID] = a
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				f.cancel = cancel
			}
			out, p := Lookup(ctx, f, f, account, Selection{TraceID: trace, AppIDs: []string{appID}, Limit: 1}, 3)
			if mode == "foreign_app" || mode == "foreign_row" || mode == "changed_app" || mode == "cancelled" {
				if p == nil {
					t.Fatal("identity/cancellation failure released evidence")
				}
				if mode == "foreign_app" && f.reads != 0 {
					t.Fatal("foreign selection queried evidence")
				}
				if mode == "cancelled" && f.reads != 1 {
					t.Fatal("cancelled lookup read later source")
				}
				return
			}
			if p != nil || out.Partial != (f.requestError || f.invocationError) || out.RequestsTruncated == f.requestError || len(out.Invocations) != boolInt(!f.invocationError) {
				t.Fatalf("source semantics lost: %+v %v", out, p)
			}
			if out.RequestsStatus == "unavailable" && len(out.Requests) != 0 || out.InvocationsStatus == "unavailable" && len(out.Invocations) != 0 {
				t.Fatal("failed source retained evidence")
			}
			if !f.invocationError && out.Invocations[0].CreatedAt > out.RequestWindowStart {
				t.Fatal("durable history was filtered by request retention")
			}
		})
	}
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
