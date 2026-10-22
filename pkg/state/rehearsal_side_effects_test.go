// adr: 20261019180000
package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A contained member can never reach real managed PostgreSQL or object
// storage, so enrollment refuses it up front; a capture-only member keeps its
// ordinary network and may still hold such bindings.
func TestMemContainedEnrollmentRefusesManagedBindings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		network string
		secret  AppSecret
		want    error
	}{
		{"contained with managed postgres", ScenarioNetworkContained, AppSecret{ManagedPostgresBindingID: uuid.NewString()}, ErrScenarioManagedBindings},
		{"contained with managed bucket", ScenarioNetworkContained, AppSecret{ManagedObjectStorageCredentialID: uuid.NewString()}, ErrScenarioManagedBindings},
		{"contained with an ordinary secret", ScenarioNetworkContained, AppSecret{}, nil},
		{"capture-only with managed postgres", "", AppSecret{ManagedPostgresBindingID: uuid.NewString()}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := NewMemStore()
			acct, err := m.CreateAccount(ctx, uuid.NewString()+"@rehearsal.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour)
			app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "member-" + uuid.NewString()[:8], Type: AppTypeApp, RAMMB: 128,
				PreviewOfSlug: "source", PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			m.secrets[secretKey{AppID: app.ID, Scope: "default", Key: "DATABASE_URL"}] = tc.secret
			m.mu.Unlock()
			run := strings.ReplaceAll(uuid.NewString(), "-", "")
			err = m.RegisterScenarioTestMembers(ctx, acct.ID, run, []ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: ScenarioWebhookCaptureMode, NetworkMode: tc.network}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("RegisterScenarioTestMembers = %v, want %v", err, tc.want)
			}
		})
	}
}
