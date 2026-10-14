package managedpostgres

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

const OperatorTestPathEnv = "FAAS_MANAGED_POSTGRES_OPERATOR_TEST_PATH"

// OperatorTestApproval authorizes an explicitly requested, short operator
// test. It is not provider qualification evidence and cannot be consumed as
// a QualificationArtifact. The deployment owner controls this private file.
type OperatorTestApproval struct {
	Version                      int       `json:"version"`
	Purpose                      string    `json:"purpose"`
	BackendID                    string    `json:"backend_id"`
	BackendFingerprint           string    `json:"backend_fingerprint"`
	CanaryAccounts               []string  `json:"canary_accounts"`
	StartedAt                    time.Time `json:"started_at"`
	ExpiresAt                    time.Time `json:"expires_at"`
	AllowPendingFinalCorrections bool      `json:"allow_pending_final_corrections,omitempty"`
}

func loadOperatorTestApproval(path string) (*OperatorTestApproval, error) {
	file, err := os.Open(path) //nolint:forbidigo // operator-owned deployment approval
	if err != nil {
		return nil, ErrInvalid
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 || info.Mode().Perm()&0o022 != 0 {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var approval OperatorTestApproval
	if decoder.Decode(&approval) != nil {
		return nil, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	return &approval, nil
}

func (a *OperatorTestApproval) valid(registry *Registry, accounts []string, now time.Time) bool {
	if a == nil || registry == nil || a.Version != 1 || a.Purpose != "operator_test" || len(accounts) == 0 || len(a.CanaryAccounts) == 0 || validateCanaryAccounts(a.CanaryAccounts) != nil || !sameCanaryAccounts(accounts, a.CanaryAccounts) {
		return false
	}
	if a.StartedAt.IsZero() || a.StartedAt.After(now) || !a.ExpiresAt.After(now) || !a.ExpiresAt.After(a.StartedAt) || a.ExpiresAt.Sub(a.StartedAt) > 3*time.Hour {
		return false
	}
	if len(registry.Regions()) != 1 {
		return false
	}
	backend, err := registry.Default(registry.DefaultRegion)
	return err == nil && strings.TrimSpace(a.BackendID) == backend.ID && a.BackendFingerprint == backend.Fingerprint
}
