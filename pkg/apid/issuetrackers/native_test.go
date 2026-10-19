package issuetrackers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type fakeHost struct {
	p                Prepared
	prepares, tokens int
	changed          bool
}

func (h *fakeHost) Prepare(context.Context) (Prepared, error) {
	h.prepares++
	p := h.p
	if h.changed && h.prepares > 1 {
		p.Connection.InstallationID++
	}
	return p, nil
}
func (h *fakeHost) Token(context.Context, Connection) (string, error) {
	h.tokens++
	return "native-secret", nil
}

type fakeProvider struct {
	status        issuetracker.Status
	reads, writes int
	err           error
}

func (p *fakeProvider) ReadStatus(_ context.Context, _ string, token string) (issuetracker.Status, error) {
	p.reads++
	if token != "native-secret" {
		return issuetracker.Status{}, errors.New("missing token")
	}
	return p.status, p.err
}
func (p *fakeProvider) SetStatus(_ context.Context, expected issuetracker.Status, token, wanted, _ string) (issuetracker.Status, error) {
	p.writes++
	if token != "native-secret" {
		return issuetracker.Status{}, errors.New("missing token")
	}
	if p.err != nil {
		return issuetracker.Status{}, p.err
	}
	if p.status != expected {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	p.status.State = wanted
	return p.status, nil
}
func fixture() (*fakeHost, *fakeProvider) {
	host := &fakeHost{p: Prepared{Issue: api.Issue{ID: "issue", AppID: "app", State: "open"}, Link: api.IssueTrackerLink{ID: "link", Provider: "github", URL: "https://github.com/owner/repo/issues/42"}, Connection: Connection{AccountID: "account", BindingID: "binding", Repository: "owner/repo", InstallationID: 1, RepositoryID: 2, OwnerID: 3}}}
	return host, &fakeProvider{status: issuetracker.Status{Provider: "github", URL: host.p.Link.URL, ID: "42", State: "resolved"}}
}
func TestNativeStatusReadsAndPushesWithoutReturningCredentials(t *testing.T) {
	host, provider := fixture()
	service := Service{Host: host, Provider: provider}
	preview, err := service.Read(context.Background())
	if err != nil || preview.Status.State != "resolved" || preview.RepositoryID != 2 {
		t.Fatal("read failed", preview, err)
	}
	result, err := service.Push(context.Background(), api.UpdateNativeIssueTrackerStatusRequest{DesiredState: "open", Expected: preview.Status})
	if err != nil || result.Status.State != "open" || provider.writes != 1 {
		t.Fatal("push failed", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "native-secret") {
		t.Fatal("credential returned")
	}
}
func TestNativeStatusRefusesScopeMismatchAndChangedConnectionsBeforeProvider(t *testing.T) {
	for _, mode := range []string{"wrong-repo", "wrong-provider", "unverified", "rebound"} {
		t.Run(mode, func(t *testing.T) {
			host, provider := fixture()
			switch mode {
			case "wrong-repo":
				host.p.Connection.Repository = "other/repo"
			case "wrong-provider":
				host.p.Link.Provider = "linear"
			case "unverified":
				host.p.Connection.RepositoryID = 0
			case "rebound":
				host.changed = true
			}
			_, err := (Service{Host: host, Provider: provider}).Read(context.Background())
			if err == nil || provider.reads != 0 || provider.writes != 0 {
				t.Fatal("invalid scope reached provider", err)
			}
		})
	}
}
func TestNativePushCannotSelectLifecycleDifferentFromGregaleOrSuppressProviderErrors(t *testing.T) {
	host, provider := fixture()
	service := Service{Host: host, Provider: provider}
	expected := fromStatus(provider.status)
	if _, err := service.Push(context.Background(), api.UpdateNativeIssueTrackerStatusRequest{DesiredState: "resolved", Expected: expected}); err == nil || host.tokens != 0 || provider.writes != 0 {
		t.Fatal("arbitrary remote lifecycle accepted")
	}
	provider.err = errors.New("native-secret provider diagnostics")
	_, err := service.Push(context.Background(), api.UpdateNativeIssueTrackerStatusRequest{DesiredState: "open", Expected: expected})
	if err == nil || strings.Contains(err.Error(), "native-secret") {
		t.Fatal("provider error leaked credentials")
	}
}
