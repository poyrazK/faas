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
