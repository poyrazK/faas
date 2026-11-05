package dataapiusage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/dataapiusage"
)

// The latest delivery report per app is kept; a runtime that has not
// published keeps the last publication another runtime reported.
func TestChangeHookReportsKeepTheLatestPerApp(t *testing.T) {
	store := dataapiusage.New(open(t))
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if got, err := store.ChangeHooks(ctx, app); err != nil || got != nil {
		t.Fatalf("before any report: %+v %v", got, err)
	}
	oldest, published := now.Add(-time.Hour), now.Add(-time.Minute)
	first := api.DataAPIChangeHookReport{Version: 1, Pending: 3, Failed: 1, OldestPendingAt: &oldest, LastPublishedAt: &published,
		LastFailureCode: "event_schema_invalid", Publishing: api.DataAPIChangeHooksPublishingRetrying}
	if err := store.RecordChangeHooks(ctx, account, app, first, now); err != nil {
		t.Fatal(err)
	}
	got, err := store.ChangeHooks(ctx, app)
	if err != nil || got == nil || got.Pending != 3 || got.Failed != 1 || !got.ReportedAt.Equal(now) || !got.OldestPendingAt.Equal(oldest) ||
		!got.LastPublishedAt.Equal(published) || got.LastFailureCode != "event_schema_invalid" || got.Publishing != api.DataAPIChangeHooksPublishingRetrying {
		t.Fatalf("first report: %+v %v", got, err)
	}
	drained := api.DataAPIChangeHookReport{Version: 1, Publishing: api.DataAPIChangeHooksPublishingOK}
	if err := store.RecordChangeHooks(ctx, account, app, drained, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = store.ChangeHooks(ctx, app)
	if err != nil || got.Pending != 0 || got.Failed != 0 || got.OldestPendingAt != nil || got.LastFailureCode != "" ||
		got.LastPublishedAt == nil || !got.LastPublishedAt.Equal(published) || !got.ReportedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("a later report from a runtime that has not published: %+v %v", got, err)
	}
	for name, tc := range map[string]struct{ account, app string }{"other account": {other, app}, "deleted app": {account, gone}} {
		if err := store.RecordChangeHooks(ctx, tc.account, tc.app, drained, now); !errors.Is(err, dataapiusage.ErrNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// The table refuses what the report contract refuses.
func TestChangeHookReportConstraints(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	for name, insert := range map[string]string{
		"pending without oldest": `INSERT INTO data_api_change_hook_reports (app_id, reported_at, pending, failed, publishing) VALUES ($1, now(), 1, 0, 'ok')`,
		"negative":               `INSERT INTO data_api_change_hook_reports (app_id, reported_at, pending, failed, publishing) VALUES ($1, now(), 0, -1, 'ok')`,
		"publishing":             `INSERT INTO data_api_change_hook_reports (app_id, reported_at, pending, failed, publishing) VALUES ($1, now(), 0, 0, 'stuck')`,
		"code":                   `INSERT INTO data_api_change_hook_reports (app_id, reported_at, pending, failed, last_failure_code, publishing) VALUES ($1, now(), 0, 1, 'Bad Code', 'ok')`,
	} {
		if _, err := pool.Exec(ctx, insert, app); err == nil {
			t.Fatalf("%s was stored", name)
		}
	}
}
