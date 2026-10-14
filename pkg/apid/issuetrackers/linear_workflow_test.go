package issuetrackers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type workflowFake struct {
	*linearStatusFake
	calls   int
	team    string
	mapping LinearWorkflowMapping
	hook    func()
	err     error
}

func (p *workflowFake) VerifyLinearWorkflowState(_ context.Context, token, team, id, state string) error {
	p.calls++
	if token != "secret" || team != p.team || (state == "open" && id != p.mapping.OpenStateID) || (state == "resolved" && id != p.mapping.ResolvedStateID) {
		return errors.New("incorrect workflow scope")
	}
	if p.hook != nil {
		p.hook()
	}
	return p.err
}
func TestLinearWorkflowMappingVerification(t *testing.T) {
	for _, kind := range []string{"valid", "invalid", "same", "provider failure", "revision race", "group race", "read race"} {
		t.Run(kind, func(t *testing.T) {
			s, host, status := linearStatusFixture()
			mapping := LinearWorkflowMapping{OpenStateID: uuid.NewString(), ResolvedStateID: uuid.NewString()}
			provider := &workflowFake{linearStatusFake: status, team: status.status.TeamID, mapping: mapping}
			s.Provider = provider
			switch kind {
			case "invalid":
				mapping.OpenStateID = "invalid"
			case "same":
				mapping.ResolvedStateID = mapping.OpenStateID
			case "provider failure":
				provider.err = errors.New("secret provider diagnostics")
			case "revision race":
				provider.hook = func() { host.p.Connection.Linear.Revision++ }
			case "group race":
				provider.hook = func() { host.p.GroupFingerprint = strings.Repeat("b", 64) }
			case "read race":
				status.readHook = func() { host.p.Connection.Linear.Revision++ }
			}
			observed, err := s.VerifyWorkflowMapping(t.Context(), mapping)
			if kind == "valid" {
				if err != nil || provider.calls != 2 || observed.Status.StateID != status.status.StateID {
					t.Fatal(observed, err, provider.calls)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid or stale mapping accepted", err)
			}
			if status.writes != 0 {
				t.Fatal("read-only mapping verification mutated provider")
			}
			if (kind == "invalid" || kind == "same") && provider.calls != 0 {
				t.Fatal("invalid mapping contacted provider")
			}
		})
	}
}

type workflowCatalogFake struct {
	*linearStatusFake
	catalog []issuetracker.LinearWorkflowState
	hook    func()
}

func (p *workflowCatalogFake) ListLinearWorkflowStates(_ context.Context, token, team string) ([]issuetracker.LinearWorkflowState, error) {
	if token != "secret" {
		return nil, errors.New("wrong key")
	}
	if p.hook != nil {
		p.hook()
	}
	return p.catalog, nil
}
func TestLinearWorkflowCatalogPinsConnectionAndGroupThroughNativeRead(t *testing.T) {
	for _, kind := range []string{"valid", "revision", "group", "read race", "foreign team", "duplicate state"} {
		t.Run(kind, func(t *testing.T) {
			service, host, status := linearStatusFixture()
			provider := &workflowCatalogFake{linearStatusFake: status, catalog: []issuetracker.LinearWorkflowState{{ID: uuid.NewString(), TeamID: status.status.TeamID, Name: "Doing", Type: "started", State: "open"}}}
			service.Provider = provider
			switch kind {
			case "revision":
				provider.hook = func() { host.p.Connection.Linear.Revision++ }
			case "group":
				provider.hook = func() { host.p.GroupFingerprint = strings.Repeat("b", 64) }
			case "read race":
				status.readHook = func() { host.p.Connection.Linear.Revision++ }
			case "foreign team":
				provider.catalog[0].TeamID = uuid.NewString()
			case "duplicate state":
				provider.catalog = append(provider.catalog, provider.catalog[0])
			}
			observed, catalog, err := service.ReadWithWorkflows(t.Context())
			if kind == "valid" {
				if err != nil || observed.ConnectionRevision != 1 || len(catalog) != 1 {
					t.Fatal(observed, catalog, err)
				}
			} else if err == nil || catalog != nil {
				t.Fatal("changed catalog scope accepted", err)
			}
			if status.writes != 0 {
				t.Fatal("catalog discovery mutated provider")
			}
		})
	}
}
