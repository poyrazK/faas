package apidserver_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProductEmail_SealsToTheAccountDomain configures product email the way
// apid boots it and checks its seal and open in domain-only mode (ADR
// 20261022111812): a new value opens only with the domain identity, and a
// value sealed before the domain still opens.
func TestProductEmail_SealsToTheAccountDomain(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	fleet, domain := mustSealIdentity(t), mustSealIdentity(t)
	withAccountDomain(t, fleet, true, domain, sealdomain.WriteDomain)
	s := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	if err := s.ConfigureProductEmail(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	p := s.ProductControl()
	if p.EmailReady == nil || !p.EmailReady() {
		t.Fatal("product email is not ready with the account domain staged")
	}
	id, token := uuid.NewString(), strings.Repeat("A", 43)
	sealed, err := p.EmailSeal(id, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.Open(fleet, sealed); err == nil {
		t.Fatal("the fleet identity opens a product email value sealed in domain mode")
	}
	if got, err := p.EmailOpen(id, sealed); err != nil || got != token {
		t.Fatalf("domain value open = %q, %v", got, err)
	}
	old, err := productemail.Seal(fleet, id, token)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.EmailOpen(id, old); err != nil || got != token {
		t.Fatalf("pre-domain value open = %q, %v", got, err)
	}
}
