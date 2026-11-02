// adr: 20261101160300
package githubdgrpc_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

func newStubClient(t *testing.T, svc *stubSvc) *githubdgrpc.Client {
	t.Helper()
	srv := grpc.NewServer()
	githubdgrpc.New(svc, wire.NewOpsMetrics("githubd_choice_stub"), nil).Register(srv)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return githubdgrpc.NewClient(conn)
}

// The installations a GitHub user may connect cross from githubd to apid
// beside the problem, so the wire shape of ExchangeOAuthCode is unchanged.
func TestExchangeOAuthCodeCarriesInstallationChoice(t *testing.T) {
	candidates := []githubdgrpc.InstallationCandidate{
		{ID: 77, Account: "octocat", AccountType: "User"},
		{ID: 88, Account: "acme", AccountType: "Organization"},
	}
	client := newStubClient(t, &stubSvc{exchangeOAuthCode: func(string, string, string) (string, string, error) {
		return "", "", &githubdgrpc.InstallationChoiceError{Candidates: candidates}
	}})
	_, _, err := client.ExchangeOAuthCode(context.Background(), "acct-1", "code", "state")
	choice, ok := githubdgrpc.AsInstallationChoice(err)
	if !ok || !reflect.DeepEqual(choice.Candidates, candidates) {
		t.Fatalf("err = %v, want the choice %v", err, candidates)
	}

	refusal := newStubClient(t, &stubSvc{exchangeOAuthCode: func(string, string, string) (string, string, error) {
		return "", "", api.ErrGitHubOrgAdminRequired("acme")
	}})
	_, _, err = refusal.ExchangeOAuthCode(context.Background(), "acct-1", "code", "state")
	var p *api.Problem
	if !errors.As(err, &p) || p.Code != api.CodeGitHubOrgAdminRequired || p.Status != 403 {
		t.Fatalf("err = %#v, want the typed 403 problem", err)
	}
}
