package githubdgrpc

// Connect GitHub with several connectable installations
// (ADR-20261101160300). githubd lists the installations the authorizing
// GitHub user may connect; apid shows them and restarts the connection with
// the chosen one. The list travels as ErrorInfo metadata on the gRPC status,
// so the ExchangeOAuthCode wire shape is unchanged.

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

// metadataInstallationCandidates is the ErrorInfo metadata key.
const metadataInstallationCandidates = "github_installation_candidates"

// MaxInstallationCandidates bounds the list a chooser shows.
const MaxInstallationCandidates = 20

// InstallationCandidate is one installation the GitHub user may connect.
type InstallationCandidate struct {
	ID          int64  `json:"id"`
	Account     string `json:"account"`
	AccountType string `json:"account_type,omitempty"`
}

// InstallationChoiceError reports that the GitHub user may connect more than
// one installation and must choose one.
type InstallationChoiceError struct {
	Candidates []InstallationCandidate
}

func (e *InstallationChoiceError) Error() string {
	return fmt.Sprintf("githubd: %d GitHub App installations can be connected; choose one", len(e.Candidates))
}

// Problem is the customer-facing problem for the choice.
func (e *InstallationChoiceError) Problem() *api.Problem {
	return api.ErrGitHubInstallationChoiceRequired(len(e.Candidates))
}

// AsInstallationChoice returns the choice carried by err, from githubd's
// service or lifted from a gRPC status.
func AsInstallationChoice(err error) (*InstallationChoiceError, bool) {
	var choice *InstallationChoiceError
	if errors.As(err, &choice) {
		return choice, true
	}
	return nil, false
}

func installationChoiceStatus(choice *InstallationChoiceError) error {
	candidates := choice.Candidates
	if len(candidates) > MaxInstallationCandidates {
		candidates = candidates[:MaxInstallationCandidates]
	}
	raw, err := json.Marshal(candidates)
	if err != nil {
		return grpcerr.ToStatus(choice.Problem())
	}
	return grpcerr.ToStatusWithMetadata(choice.Problem(), map[string]string{metadataInstallationCandidates: string(raw)})
}

// liftInstallationChoice restores the choice from a gRPC status error.
func liftInstallationChoice(err error) (*InstallationChoiceError, bool) {
	raw, ok := grpcerr.MetadataValue(err, metadataInstallationCandidates)
	if !ok {
		return nil, false
	}
	var candidates []InstallationCandidate
	decoded := json.Unmarshal([]byte(raw), &candidates) == nil && len(candidates) > 0
	if !decoded {
		return nil, false
	}
	return &InstallationChoiceError{Candidates: candidates}, true
}
