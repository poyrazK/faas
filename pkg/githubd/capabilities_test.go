// adr: 20261023115741 — githubd ignores GitHub work for a capability whose
// platform flag is off for the bound account.
package githubd

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// capabilitiesOff returns a decider with the named capabilities off.
func capabilitiesOff(off ...string) func(capability string, subject platformflags.Subject) bool {
	return func(capability string, _ platformflags.Subject) bool {
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

// TestScanOptionsFollowKubernetesManifestsCapability — push and preview
// scans use the account's kubernetes-manifests decision, as apid's reviewed
// scan does (ADR-20261029093621 §7).
func TestScanOptionsFollowKubernetesManifestsCapability(t *testing.T) {
	t.Parallel()
	if !(&Service{}).scanOptions(platformflags.Subject{AccountID: "acct-1"}).KubernetesManifests {
		t.Error("nil decider: KubernetesManifests = false, want released")
	}
	off := &Service{CapabilityAllowed: capabilitiesOff(reposcan.KubernetesManifestsCapability)}
	if off.scanOptions(platformflags.Subject{AccountID: "acct-1"}).KubernetesManifests {
		t.Error("capability off: KubernetesManifests = true, want false")
	}
}

// TestScanOptionsFollowPrivateServiceHTTPSCapability — a push or preview scan
// refuses the https service transport where private service HTTPS is off, as
// apid's reviewed scan does (ADR-20261029230000).
func TestScanOptionsFollowPrivateServiceHTTPSCapability(t *testing.T) {
	t.Parallel()
	if (&Service{}).scanOptions(platformflags.Subject{AccountID: "acct-1"}).HTTPSTransportUnavailable {
		t.Error("nil decider: HTTPSTransportUnavailable = true, want the transport released")
	}
	off := &Service{CapabilityAllowed: capabilitiesOff(api.PrivateServiceHTTPSCapability)}
	if !off.scanOptions(platformflags.Subject{AccountID: "acct-1"}).HTTPSTransportUnavailable {
		t.Error("capability off: HTTPSTransportUnavailable = false, want refused")
	}
}

// ADR-20261105090226: a pull request's preview flag is decided for the
// resource's organization and plan, as apid decides it, so an organization
// rule turns that company's previews off.
func TestPreviewCapabilityUsesTheOrganizationAndPlan(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "flag-subject@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var seen []platformflags.Subject
	svc := &Service{Reconcile: &reconcile.Service{Store: store}, CapabilityAllowed: func(capability string, subject platformflags.Subject) bool {
		seen = append(seen, subject)
		return subject.OrgID != "org-off"
	}}
	if !svc.previewsAllowed(svc.capabilitySubject(ctx, account.ID, "")) {
		t.Fatal("personal app: previews off, want on")
	}
	if len(seen) == 0 || seen[0].AccountID != account.ID || seen[0].Plan != string(api.PlanPro) {
		t.Fatalf("personal subject = %+v, want the account and its plan", seen)
	}
	if svc.previewsAllowed(platformflags.Subject{AccountID: account.ID, OrgID: "org-off"}) {
		t.Fatal("organization rule ignored: previews on, want off")
	}
}
