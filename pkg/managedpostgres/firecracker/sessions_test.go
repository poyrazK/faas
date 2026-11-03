// adr: 1027 — the native SessionTerminator (ADR-1370) checks its requests,
// reports presence, and treats a database that no longer exists as
// holding nothing.
package firecracker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSessionTerminatorChecksItsRequests(t *testing.T) {
	p, _, _ := newTestProvider(t)
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	for name, reqs := range map[string][]managedpostgres.CredentialRequest{
		"none":           nil,
		"two databases":  {{ProviderResourceID: a, IdentityKey: "x"}, {ProviderResourceID: b, IdentityKey: "y"}},
		"no identity":    {{ProviderResourceID: a}},
		"no database id": {{IdentityKey: "x"}},
	} {
		if err := p.TerminateSessions(ctx, reqs); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("%s: terminate = %v, want ErrInvalid", name, err)
		}
		if _, err := p.ObserveSessions(ctx, reqs); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("%s: observe = %v, want ErrInvalid", name, err)
		}
	}
	// A database schedd no longer has holds no sessions.
	gone := []managedpostgres.CredentialRequest{{ProviderResourceID: uuid.NewString(), IdentityKey: "x", Access: managedpostgres.CredentialReadWrite}}
	if err := p.TerminateSessions(ctx, gone); err != nil {
		t.Fatalf("terminate on a deleted database = %v", err)
	}
	if obs, err := p.ObserveSessions(ctx, gone); err != nil || obs != (managedpostgres.SessionObservation{}) {
		t.Fatalf("observe on a deleted database = %+v, %v", obs, err)
	}
}

func TestObserveSessionsReportsPresence(t *testing.T) {
	p, sched, _ := newTestProvider(t)
	ctx := context.Background()
	id := provisionTestDatabase(t, p)
	reqs := []managedpostgres.CredentialRequest{
		{ProviderResourceID: id, IdentityKey: "rw", Access: managedpostgres.CredentialReadWrite},
		{ProviderResourceID: id, IdentityKey: "rw", Access: managedpostgres.CredentialReadWrite}, // a repeat names one role
		{ProviderResourceID: id, IdentityKey: "ro", Access: managedpostgres.CredentialReadOnly},
	}
	remaining := map[string]bool{sqlstateSessionsRemain: true, sqlstatePreparedRemain: false}
	sched.mu.Lock()
	sched.adminRun = func(sql string) (*scheddpb.DatabaseAdminResponse, error) {
		for state, remains := range remaining {
			if remains && strings.Contains(sql, "'"+state+"'") {
				return &scheddpb.DatabaseAdminResponse{ErrorCode: "sql_failed", Sqlstate: state}, nil
			}
		}
		return &scheddpb.DatabaseAdminResponse{}, nil
	}
	sched.mu.Unlock()
	obs, err := p.ObserveSessions(ctx, reqs)
	if err != nil || obs != (managedpostgres.SessionObservation{Sessions: 1}) {
		t.Fatalf("observe = %+v, %v; want sessions present", obs, err)
	}
	remaining[sqlstateSessionsRemain], remaining[sqlstatePreparedRemain] = false, true
	if obs, err := p.ObserveSessions(ctx, reqs); err != nil || obs != (managedpostgres.SessionObservation{PreparedTransactions: 1}) {
		t.Fatalf("observe = %+v, %v; want a prepared transaction present", obs, err)
	}
	sched.mu.Lock()
	last := sched.admin[len(sched.admin)-1]
	sched.mu.Unlock()
	rw := credentialRoleName(id, "rw", managedpostgres.CredentialReadWrite)
	if strings.Count(last, rw) != 1 {
		t.Fatalf("a repeated identity was checked %d times:\n%s", strings.Count(last, rw), last)
	}
}

func provisionTestDatabase(t *testing.T, p *Provider) string {
	t.Helper()
	obs, err := p.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: uuid.NewString(), Spec: devSpec()})
	if err != nil {
		t.Fatal(err)
	}
	return obs.ProviderResourceID
}
