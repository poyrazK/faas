//go:build linux || darwin

package main

// adr: 632

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type signingPeerResolver struct{}

func (signingPeerResolver) NodeIDByCN(string) (string, error) { return "", runtimequota.ErrTrust }

func TestRegionalRuntimeSigningConfigurationAndCellBinding(t *testing.T) {
	cell := db.CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	dir := filepath.Join(t.TempDir(), "cell")
	if _, err := runtimequota.ProvisionAuthorityKeys(dir, runtimequota.Cell, cell.ID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schedd.toml")
	body := "[regional_runtime_signing]\nprivate_key_path = '" + filepath.Join(dir, runtimequota.PrivateKeyFilename) + "'\ntrust_bundle_path = '" + filepath.Join(dir, runtimequota.TrustFilename) + "'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || !cfg.RegionalRuntimeSigning.Configured() {
		t.Fatal("TOML configuration lost", err)
	}
	store := state.NewMemStore()
	router := sched.NewVMMRouter(nil, nil, nil).WithRegionalRuntimePeerResolver(signingPeerResolver{})
	owner, err := regionalRetirementForCell(*cfg, cell, store, router)
	if err != nil || owner.Signer.IssuerID() != cell.ID {
		t.Fatal("bound signer failed", err)
	}
	for _, wrong := range []db.CellIdentity{{ID: uuid.NewString(), Region: cell.Region}, {ID: cell.ID}, {ID: cell.ID, Region: "INVALID"}} {
		if _, err := regionalRetirementForCell(*cfg, wrong, store, router); err == nil {
			t.Fatal("accepted different or unbound cell", wrong)
		}
	}
	if _, err := loadRegionalRuntimeRetirement(t.Context(), *cfg, nil, store, router, false); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("missing authenticated transport accepted", err)
	}
	if got, err := loadRegionalRuntimeRetirement(t.Context(), Config{}, nil, nil, nil, false); err != nil || got != nil {
		t.Fatal("legacy unconfigured startup changed", err)
	}
}
