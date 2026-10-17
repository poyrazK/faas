package issuetrackers

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"strings"
	"testing"
)

type linearStatusFake struct {
	status              issuetracker.Status
	reads, writes       int
	err                 error
	readHook, writeHook func()
}

func (p *linearStatusFake) ReadStatus(context.Context, string, string) (issuetracker.Status, error) {
	p.reads++
	if p.readHook != nil {
		p.readHook()
	}
	return p.status, p.err
}
func (p *linearStatusFake) SetStatus(_ context.Context, expected issuetracker.Status, _, wanted, state string) (issuetracker.Status, error) {
	if p.err != nil {
		return issuetracker.Status{}, p.err
	}
	if p.status != expected {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	if p.status.State != wanted {
		p.writes++
		p.status.State = wanted
		p.status.StateID = state
	}
	if p.writeHook != nil {
		p.writeHook()
	}
	return p.status, nil
}
func linearStatusFixture() (LinearService, *creationHostFake, *linearStatusFake) {
	_, host, _, _, _ := linearCreationFixture()
	host.p.GroupFingerprint = strings.Repeat("a", 64)
	host.p.Link = api.IssueTrackerLink{ID: uuid.NewString(), Provider: "linear", URL: "https://linear.app/acme/issue/PAY-42"}
	provider := &linearStatusFake{status: issuetracker.Status{Provider: "linear", URL: host.p.Link.URL, ID: uuid.NewString(), TeamID: host.p.Connection.Linear.Scope.Identity.TeamID, StateID: uuid.NewString(), State: "resolved"}}
	return LinearService{Host: host, Provider: provider}, host, provider
}
func linearPushInput(preview api.NativeLinearIssueTrackerStatus) api.UpdateNativeLinearIssueTrackerStatusRequest {
	return api.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: "open", Expected: preview.Status, ExpectedConnectionRevision: preview.ConnectionRevision, ExpectedGroupFingerprint: preview.GroupFingerprint, LinearStateID: uuid.NewString()}
}
func TestNativeLinearStatusPreviewPushAndNoOp(t *testing.T) {
	service, _, provider := linearStatusFixture()
	preview, err := service.Read(t.Context())
	if err != nil || preview.TeamID != provider.status.TeamID || preview.ConnectionRevision != 1 {
		t.Fatal(preview, err)
	}
	input := linearPushInput(preview)
	result, err := service.Push(t.Context(), input)
	if err != nil || result.Status.State != "open" || result.Status.StateID != input.LinearStateID || provider.writes != 1 {
		t.Fatal(result, err)
	}
	if _, err := service.Push(t.Context(), input); err == nil || provider.writes != 1 {
		t.Fatal("stale status wrote again")
	}
	input = linearPushInput(result)
	input.LinearStateID = ""
	if _, err := service.Push(t.Context(), input); err != nil || provider.writes != 1 {
		t.Fatal("aligned no-op mutated", err)
	}
}
func TestNativeLinearStatusFencesPreviewAndInFlightScope(t *testing.T) {
	for _, kind := range []string{"revision", "group", "state", "team", "workflow", "credential race", "read race", "write race", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			service, host, provider := linearStatusFixture()
			preview, err := service.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			input := linearPushInput(preview)
			switch kind {
			case "revision":
				host.p.Connection.Linear.Revision++
			case "group":
				host.p.GroupFingerprint = strings.Repeat("b", 64)
			case "state":
				host.p.Issue.State = "ignored"
			case "team":
				input.Expected.TeamID = uuid.NewString()
			case "workflow":
				input.LinearStateID = ""
			case "credential race":
				host.tokenHook = func() { host.p.Connection.Linear.Revision++ }
			case "read race":
				provider.readHook = func() { host.p.GroupFingerprint = strings.Repeat("b", 64) }
			case "write race":
				provider.writeHook = func() { host.p.GroupFingerprint = strings.Repeat("b", 64) }
			case "unsupported":
				provider.err = issuetracker.ErrUnsupportedStatus
			}
			if kind == "read race" {
				_, err = service.Read(t.Context())
			} else {
				_, err = service.Push(t.Context(), input)
			}
			if err == nil {
				t.Fatal("changed scope or unsupported request accepted")
			}
			want := 0
			if kind == "write race" {
				want = 1
			}
			if provider.writes != want {
				t.Fatal("unexpected provider mutation", provider.writes)
			}
		})
	}
}
func TestNativeLinearStatusSanitizesProviderDiagnostics(t *testing.T) {
	service, _, provider := linearStatusFixture()
	provider.err = errors.New("secret provider diagnostics")
	if _, err := service.Read(t.Context()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("provider diagnostic leaked", err)
	}
}
