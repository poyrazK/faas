package deploystaging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

type ProjectCriticalProbeSender interface {
	Configured() bool
	ProbeCritical(context.Context, string, string, string, string, string) (routeprobe.Outcome, error)
}

// ProbeProjectRelease sends only configured read-only probes using private,
// deployment-scoped challenges. Infrastructure failures leave health unknown.
func ProbeProjectRelease(ctx context.Context, receipt api.ProjectSubmissionReceipt, baseline state.ProjectReleaseSet, sender ProjectCriticalProbeSender, notify func(context.Context, string) error, challengeDelay time.Duration) ([]api.ProjectReleaseProbeEvidence, error) {
	return ProbeProjectReleaseForScope(ctx, "production", receipt, baseline, sender, notify, challengeDelay)
}

// ProbeProjectReleaseForScope keeps destination scope private and bound to the
// challenge, including when a zero-traffic candidate needs a cold wake.
func ProbeProjectReleaseForScope(ctx context.Context, scope string, receipt api.ProjectSubmissionReceipt, baseline state.ProjectReleaseSet, sender ProjectCriticalProbeSender, notify func(context.Context, string) error, challengeDelay time.Duration) ([]api.ProjectReleaseProbeEvidence, error) {
	if !api.ValidProjectEnvironmentSlug(scope) || receipt.Apply.SafeRelease == nil || receipt.Apply.SafeRelease.Verification == nil {
		return nil, fmt.Errorf("invalid critical probe contract")
	}
	if sender == nil || !sender.Configured() || notify == nil {
		return nil, fmt.Errorf("critical gateway probes unavailable")
	}

	candidates, stable := map[string]string{}, map[string]string{}
	for _, build := range receipt.Apply.Builds {
		candidates[build.AppID] = build.DeploymentID
	}
	for _, member := range baseline.Members {
		stable[member.AppID] = member.DeploymentID
	}
	tokens := map[string]string{}
	for _, p := range receipt.Apply.SafeRelease.Verification.Probes {
		for _, id := range []string{candidates[p.AppID], stable[p.AppID]} {
			if id == "" {
				return nil, fmt.Errorf("verification member missing")
			}
			if tokens[id] != "" {
				continue
			}
			token, err := routeprobe.NewToken()
			if err != nil {
				return nil, err
			}
			payload, err := json.Marshal(struct {
				Scope        string    `json:"scope"`
				AppID        string    `json:"app_id"`
				DeploymentID string    `json:"deployment_id"`
				Token        string    `json:"token"`
				ExpiresAt    time.Time `json:"expires_at"`
			}{scope, p.AppID, id, token, time.Now().UTC().Add(api.RouteHealthProbeChallengeTTL)})
			if err != nil {
				return nil, err
			}
			if err := notify(ctx, string(payload)); err != nil {
				return nil, err
			}
			tokens[id] = token
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(challengeDelay):
	}
	// Existing sender bounds concurrency, duration, auth handling and attribution.
	var evidence []api.ProjectReleaseProbeEvidence
	for _, p := range receipt.Apply.SafeRelease.Verification.Probes {
		e := api.ProjectReleaseProbeEvidence{Probe: p}
		sampleProjectReleaseProbe(ctx, sender, p, candidates[p.AppID], stable[p.AppID], tokens, &e)
		evidence = append(evidence, e)
	}
	return evidence, nil
}

func sampleProjectReleaseProbe(ctx context.Context, sender ProjectCriticalProbeSender, probe api.ProjectReleaseProbe, candidate, stable string, tokens map[string]string, evidence *api.ProjectReleaseProbeEvidence) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, 4)
	targets := []string{candidate, stable}
	if candidate == stable {
		targets = targets[:1]
	}
	for _, id := range targets {
		for range api.RouteHealthProbeRequestsPerMinute {
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				wg.Wait()
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-semaphore }()
				requestCtx, cancel := context.WithTimeout(ctx, api.RouteHealthProbeRequestTimeout)
				defer cancel()
				outcome, err := sender.ProbeCritical(requestCtx, probe.Slug, id, tokens[id], probe.Method, probe.Path)
				if err != nil || outcome == routeprobe.Unattributed {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if id == candidate {
					evidence.CandidateRequests++
					if outcome == routeprobe.ServerError {
						evidence.CandidateErrors++
					}
				}
				if id == stable {
					evidence.StableRequests++
					if outcome == routeprobe.ServerError {
						evidence.StableErrors++
					}
				}
				if outcome == routeprobe.Unauthenticated {
					evidence.Unauthenticated++
				}
				if outcome == routeprobe.UnexpectedStatus {
					evidence.UnexpectedResponses++
				}
			}()
		}
	}
	wg.Wait()
}
