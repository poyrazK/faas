package issuetrackers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
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
