package fcvm

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// adr: 20261023173624 — vmmd opens a bound runtime secret only for its own account.

func TestSealedEnvBindingGuardsTheWorkloadAccount(t *testing.T) {
	id := newIdentity(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetHostIdentity(id)
	bound, err := secretbox.SealOneBound(id.Recipient(), secretbox.Binding{AccountID: "acct-owner"}, "DB_URL", "postgres://owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	unbound := sealEnv(t, id, secretbox.Envelope{"LEGACY": "v"})
	entries := []SealedEnvEntry{{Key: "DB_URL", SourceKey: "DB_URL", Ciphertext: bound}, {Key: "LEGACY", SourceKey: "LEGACY", Ciphertext: unbound}}

	for _, account := range []string{"acct-owner"} {
		secrets, err := m.UnsealRuntimeSecrets(entries, account)
		if err != nil || secrets["DB_URL"] != "postgres://owner" || secrets["LEGACY"] != "v" {
			t.Fatalf("UnsealRuntimeSecrets(%s) = %v, %v", account, secrets, err)
		}
		files, _, err := m.prepareWakeFiles(WakeRequest{AccountID: account, SealedEnvEntries: entries})
		if err != nil || len(files) == 0 {
			t.Fatalf("prepareWakeFiles(%s) = %s, %v", account, files, err)
		}
	}
	for _, account := range []string{"acct-other", ""} {
		secrets, err := m.UnsealRuntimeSecrets(entries, account)
		if !errors.Is(err, secretbox.ErrBindingMismatch) || len(secrets) != 0 {
			t.Errorf("UnsealRuntimeSecrets(%q) = %v, %v; want ErrBindingMismatch and no values", account, secrets, err)
		}
		files, _, err := m.prepareWakeFiles(WakeRequest{AccountID: account, SealedEnvEntries: entries})
		if !errors.Is(err, secretbox.ErrBindingMismatch) || files != nil {
			t.Errorf("prepareWakeFiles(%q) = %s, %v; want ErrBindingMismatch and no file", account, files, err)
		}
	}
	// The legacy merge path (no SourceKey) applies the same check.
	merged := []SealedEnvEntry{{Key: "DB_URL", Ciphertext: bound}}
	if _, err := m.openSealedEnvEntries(merged, "acct-other"); !errors.Is(err, secretbox.ErrBindingMismatch) {
		t.Errorf("openSealedEnvEntries(other) = %v, want ErrBindingMismatch", err)
	}
}

// With RequireSecretBinding (the contract step) vmmd also refuses unbound
// envelopes, on every path that opens runtime secrets.
func TestRequireSecretBindingRefusesUnboundEnvelopes(t *testing.T) {
	id := newIdentity(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetHostIdentity(id)
	m.RequireSecretBinding(true)
	bound, err := secretbox.SealOneBound(id.Recipient(), secretbox.Binding{AccountID: "acct-owner"}, "DB_URL", "postgres://owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	unbound := sealEnv(t, id, secretbox.Envelope{"LEGACY": "v"})
	boundOnly := []SealedEnvEntry{{Key: "DB_URL", SourceKey: "DB_URL", Ciphertext: bound}}
	if secrets, err := m.UnsealRuntimeSecrets(boundOnly, "acct-owner"); err != nil || secrets["DB_URL"] != "postgres://owner" {
		t.Fatalf("UnsealRuntimeSecrets(bound) = %v, %v", secrets, err)
	}
	if _, err := m.UnsealRuntimeSecrets(boundOnly, "acct-other"); !errors.Is(err, secretbox.ErrBindingMismatch) {
		t.Fatalf("UnsealRuntimeSecrets(bound, other account) = %v, want ErrBindingMismatch", err)
	}
	withLegacy := []SealedEnvEntry{boundOnly[0], {Key: "LEGACY", SourceKey: "LEGACY", Ciphertext: unbound}}
	if secrets, err := m.UnsealRuntimeSecrets(withLegacy, "acct-owner"); !errors.Is(err, secretbox.ErrUnbound) || len(secrets) != 0 {
		t.Errorf("UnsealRuntimeSecrets(unbound) = %v, %v; want ErrUnbound and no values", secrets, err)
	}
	if files, _, err := m.prepareWakeFiles(WakeRequest{AccountID: "acct-owner", SealedEnvEntries: withLegacy}); !errors.Is(err, secretbox.ErrUnbound) || files != nil {
		t.Errorf("prepareWakeFiles(unbound) = %s, %v; want ErrUnbound and no file", files, err)
	}
	merged := []SealedEnvEntry{{Key: "LEGACY", Ciphertext: unbound}}
	if _, err := m.openSealedEnvEntries(merged, "acct-owner"); !errors.Is(err, secretbox.ErrUnbound) {
		t.Errorf("openSealedEnvEntries(unbound) = %v, want ErrUnbound", err)
	}
	// Switched off again, the unbound envelope opens as it did before.
	m.RequireSecretBinding(false)
	if secrets, err := m.UnsealRuntimeSecrets(withLegacy, "acct-owner"); err != nil || secrets["LEGACY"] != "v" {
		t.Fatalf("UnsealRuntimeSecrets(unbound, not required) = %v, %v", secrets, err)
	}
}
