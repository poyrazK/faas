//go:build !no_pg

package receivers

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUnicodeReferrerCannotBlockOtherAppsRollups(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "rollup-unicode-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	aggregator := NewRequestAnalyticsRollups(store, nil, nil)
	aggregator.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
	var apps []state.App
	for i, slug := range []string{"unicode-dimension", "healthy-dimension"} {
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		referrer := "example.com"
		if i == 0 {
			referrer = strings.Repeat("界", 171)
		}
		row := RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: uuid.MustParse(account.ID), AppID: uuid.MustParse(app.ID), DeploymentID: uuid.New(), ReceivedAt: time.Now().UTC(), Route: "GET /", Method: "GET", ReferrerHost: referrer, Status: 200, Count: 1}
		if counted, err := aggregator.add(t.Context(), row); err != nil || !counted {
			t.Fatalf("register observation: counted=%v err=%v", counted, err)
		}
	}
	for range 2 {
		if err := aggregator.Flush(t.Context()); err != nil {
			t.Fatalf("one referrer poisoned the shared transaction: %v", err)
		}
	}
	for _, app := range apps {
		var count int64
		if err := pool.QueryRow(t.Context(), "SELECT coalesce(sum(requests),0) FROM request_analytics_minutes WHERE app_id=$1 AND dimension_kind='route'", app.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("app %s lost traffic to another app's referrer: %d", app.Slug, count)
		}
	}
}
