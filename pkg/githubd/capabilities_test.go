// adr: 20261023115741 — githubd ignores GitHub work for a capability whose
// platform flag is off for the bound account.
package githubd

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// capabilitiesOff returns a decider with the named capabilities off.
func capabilitiesOff(off ...string) func(capability, accountID string) bool {
	return func(capability, _ string) bool {
		for _, id := range off {
			if id == capability {
				return false
			}
		}
		return true
	}
}

func TestHandlePushRequest_GitHubDeploysOffIgnoresPushBeforeSourceFetch(t *testing.T) {
	rig := newRig(t, nil)
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	svc.Source = &stubSource{err: errors.New("source fetch must not run")}
	enq := &recordingEnqueuer{buildID: "build-held"}
	svc.Enqueuer = enq
	svc.CapabilityAllowed = capabilitiesOff(CapabilityGitHubDeploys)
	body := []byte(`{"ref":"refs/heads/main","after":"cafebabe","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)

	result, err := svc.HandlePushRequest(context.Background(), body)

	if !errors.Is(err, ErrIgnored) || !result.WasIgnored {
		t.Fatalf("push with github-deploys off = (%+v, %v), want ignored", result, err)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("enqueue calls = %d, want none", len(enq.calls))
	}
}

func TestHandlePullRequest_PreviewsOffIgnoresOpenButClosesExisting(t *testing.T) {
	for _, off := range []string{CapabilityPRPreviews, CapabilityGitHubDeploys} {
		t.Run(off, func(t *testing.T) {
			rig := newPreviewRig(t)
			svc, _ := newPreviewService(t, rig)
			ctx := context.Background()
			if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
				t.Fatalf("open while released: %v", err)
			}

			svc.CapabilityAllowed = capabilitiesOff(off)
			result, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(43, "feedface00000000000000000000000000000000"))
			if !errors.Is(err, ErrIgnored) || !result.WasIgnored {
				t.Fatalf("open with %s off = (%+v, %v), want ignored", off, result, err)
			}
			if _, err := svc.handlePullRequest(ctx, pullRequestClosedBody(42, "deadbeef00000000000000000000000000000000")); err != nil {
				t.Fatalf("close with %s off: %v", off, err)
			}

			previews, err := rig.mem.PreviewAppsByParent(ctx, rig.acct, "demo-app")
			if err != nil {
				t.Fatal(err)
			}
			if len(previews) != 1 || previews[0].PreviewPrNumber != 42 || previews[0].PreviewPrState != state.PreviewPrStateClosed {
				t.Fatalf("previews = %+v, want only PR 42, closed", previews)
			}
		})
	}
}
