package githubd

import (
	"context"
	"net/http"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

type investigationCancellationHTTPClient struct {
	started chan context.Context
	release chan struct{}
}

func (c *investigationCancellationHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.started <- req.Context()
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-c.release:
		return nil, context.Canceled
	}
}

func TestInvestigationRepositoryVerificationHonorsRPCCancellation(t *testing.T) {
	for _, name := range []string{"grpc-used-by-gitops", "production-recovery-wrapper", "control-context-aware-service"} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newTestBindableRealService(t, "acct-1", testInstallableRepo())
			client := &investigationCancellationHTTPClient{started: make(chan context.Context, 1), release: make(chan struct{})}
			svc.Auth.HTTPClient = client
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if name != "control-context-aware-service" {
					var service githubdgrpc.Service = svc
					if name == "production-recovery-wrapper" {
						service = NewRecoveryService(svc, nil, nil)
					}
					_, err := githubdgrpc.New(service, nil, nil).ListInstallableRepos(ctx, &githubdpb.ListInstallableReposRequest{AccountId: "acct-1", InstallationId: 1})
					done <- err
				} else {
					_, err := svc.ListInstallableReposContext(ctx, "acct-1", 1)
					done <- err
				}
			}()
			defer func() {
				close(client.release)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("cleanup timeout")
				}
			}()
			var outbound context.Context
			select {
			case outbound = <-client.started:
			case err := <-done:
				t.Fatalf("RPC did not reach GitHub: %v", err)
			case <-time.After(time.Second):
				t.Fatal("GitHub request did not start")
			}
			cancel()
			select {
			case <-outbound.Done():
				t.Log("GitHub HTTP request stopped on RPC cancellation")
			case <-time.After(40 * time.Millisecond):
				t.Error("GitHub HTTP request continues after the GitOps repository-verification RPC was canceled")
			}
		})
	}
}
