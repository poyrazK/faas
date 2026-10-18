package state

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

func TestRenewalIntentLeaseRecoveryAndFences(t *testing.T) {
	at := time.Now().UTC()
	until := at.Add(time.Minute)
	cfg := &api.MCPCatalogObserverConfig{Enabled: true, OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "client", RefreshTokenSecretRef: "secret:REFRESH"}}
	root := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: uuid.NewString(), Enabled: true, Observer: cfg}, ObserverStatus: &api.MCPCatalogObserverStatus{LeaseID: uuid.NewString(), LeaseUntil: &until}}
	job := MCPCatalogObserverJob{Role: "default", Snapshot: root, LeaseID: root.ObserverStatus.LeaseID}
	journal, fresh, err := beginOAuthRenewal(&root, job, strings.Repeat("a", 64), at)
	if err != nil || !fresh {
		t.Fatal("intent failed", err)
	}
	// No second exchange under the same lease.
	again, fresh, err := beginOAuthRenewal(&root, job, strings.Repeat("a", 64), at)
	if err != nil || fresh || again.ID != journal.ID {
		t.Fatal("intent replayed", err)
	}
	// A new lease sees unfinished issuance as unknown.
	newer := job
	newer.LeaseID = uuid.NewString()
	root.ObserverStatus.LeaseID = newer.LeaseID
	unknown, fresh, err := beginOAuthRenewal(&root, newer, strings.Repeat("a", 64), at)
	if err != nil || fresh || unknown.State != "reauthorization_required" {
		t.Fatal("unknown exchange retried", err)
	}
	// A late response from the original operation can still be recorded, but only
	// the current lease owner can install the exact result.
	expiry := at.Add(time.Hour)
	result := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: journal.SourceSHA256, Ciphertext: []byte("sealed"), ExpiresAt: &expiry, LastRenewalAt: &at}
	if err = recordOAuthRenewal(&root, job, journal.ID, "saved", &result); err != nil {
		t.Fatal("late result was lost", err)
	}
	if err = saveObserverOAuth(&root, job, result, at); !errors.Is(err, ErrMCPCatalogMonitorChanged) {
		t.Fatal("old lease installed result", err)
	}
	wrong := result
	wrong.Ciphertext = []byte("other")
	if err = saveObserverOAuth(&root, newer, wrong, at); !errors.Is(err, ErrMCPCatalogMonitorChanged) {
		t.Fatal("different result installed", err)
	}
	if err = saveObserverOAuth(&root, newer, result, at); err != nil {
		t.Fatal("current owner could not recover", err)
	}
	if root.ObserverStatus.Renewal.State != "installed" || root.ObserverStatus.Renewal.Result != nil {
		t.Fatal("installed journal retained sealed copy")
	}
	// A source change permits reauthorization and fences the old operation.
	next, fresh, err := beginOAuthRenewal(&root, newer, strings.Repeat("b", 64), at)
	if err != nil || !fresh || next.ID == journal.ID {
		t.Fatal("new source was not admitted", err)
	}
	if err = recordOAuthRenewal(&root, job, journal.ID, "saved", &result); !errors.Is(err, ErrMCPCatalogMonitorChanged) {
		t.Fatal("superseded operation recorded", err)
	}
	stale := newer
	stale.Snapshot.Config.Revision = uuid.NewString()
	if err = recordOAuthRenewal(&root, stale, next.ID, "failed", nil); !errors.Is(err, ErrMCPCatalogMonitorChanged) {
		t.Fatal("stale revision recorded", err)
	}
}
func TestRenewalExpiredSavedResultCanInstall(t *testing.T) {
	at := time.Now().UTC()
	leaseUntil := at.Add(time.Minute)
	expired := at.Add(-time.Minute)
	result := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: strings.Repeat("a", 64), Ciphertext: []byte("sealed"), ExpiresAt: &expired, LastRenewalAt: &expired}
	root := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: uuid.NewString(), Enabled: true, Observer: &api.MCPCatalogObserverConfig{Enabled: true, OAuth: &api.MCPCatalogObserverOAuthConfig{}}}, ObserverStatus: &api.MCPCatalogObserverStatus{LeaseID: uuid.NewString(), LeaseUntil: &leaseUntil, Renewal: &api.MCPCatalogOAuthRenewal{ID: uuid.NewString(), State: "saved", Result: &result}}}
	job := MCPCatalogObserverJob{Role: "default", Snapshot: root, LeaseID: root.ObserverStatus.LeaseID}
	if err := saveObserverOAuth(&root, job, result, at); err != nil {
		t.Fatal("expired result lost rotated refresh token", err)
	}
}
