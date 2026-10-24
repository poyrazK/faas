package imaged

// adr: 20261024191249

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/storagekey"
)

// ReleaseBasesResult reports the rollout pre-stage's pass over every runtime
// base the candidate release configures. Each list holds runtimes; "" is the
// minimal base.
type ReleaseBasesResult struct {
	// Staged: the shared copy named another generation and this node's
	// cache did not hold the candidate's, so this pre-stage published it.
	Staged []string
	// Current: cached here or already published at the candidate's
	// generation, so nothing was built.
	Current []string
	// Unpublished: never published by any node; the first deployment of the
	// runtime stages it, as before.
	Unpublished []string
	// Deferred: left for the serving imaged because the serving vmmd
	// predates verified parents (H10-95).
	Deferred []string
}

// EnsureReleaseBases runs in the rollout pre-stage only, after
// EnsureAssignedBases. It publishes every runtime base the candidate
// configures that this node's vmmd could not adopt, whoever owns the apps on
// it, so the node can boot the release's bases once it serves.
//
// The serving imaged keeps staging only its assigned bases: the pre-stage of
// each rolled node runs this pass first, and a serving restart must not
// rebuild bases for the whole fleet. A base already published at the
// candidate's generation, or committed in this node's cache, is not touched,
// so only the first node to roll a changed generation builds and scans it.
func (h *Handler) EnsureReleaseBases(ctx context.Context, arch string, envLookup func(string) string, assigned AssignedBasesResult) (ReleaseBasesResult, error) {
	if h == nil {
		return ReleaseBasesResult{}, fmt.Errorf("imaged: ensure release bases: handler is not configured")
	}
	be, err := h.storageFor()
	if err != nil {
		return ReleaseBasesResult{}, fmt.Errorf("imaged: ensure release bases: storage: %w", err)
	}
	handled := make(map[string]bool, len(assigned.Runtimes)+1)
	if assigned.Minimal {
		handled[""] = true
	}
	for _, runtime := range assigned.Runtimes {
		handled[runtime] = true
	}
	result, err := releaseBasePlan(GatedRuntimeBases(), handled, func(runtime string) (BaseAdoption, error) {
		ref, err := resolveDeployBaseRef(runtime, envLookup)
		if err != nil {
			return BaseAdoption{}, err
		}
		return JudgeBaseAdoption(ctx, be, storagekey.BaseForArch(runtime, arch), ref)
	})
	if err != nil {
		return ReleaseBasesResult{}, err
	}
	result.Deferred, err = stageReleaseBases(result.Staged, h.deferLegacyParentBases, func(runtime string) error {
		// The first node to roll a changed generation builds and scans it
		// here, behind its drain barrier; the duration is that node's share
		// of the join-node budget.
		started := time.Now()
		var err error
		if runtime == "" {
			_, err = h.EnsureMinimalBase(ctx, arch, envLookup)
		} else {
			_, err = h.EnsureRuntimeBase(ctx, runtime, arch, envLookup)
		}
		if err == nil {
			h.log.Info("imaged: published release runtime base",
				"runtime", releaseBaseName(runtime), "duration", time.Since(started).Round(time.Second).String())
		}
		return err
	})
	if err != nil {
		return ReleaseBasesResult{}, err
	}
	return result, nil
}

// releaseBasePlan sorts the configured runtimes the assigned pass did not
// already stage by their adoption verdict. Only stale ones are staged.
func releaseBasePlan(runtimes []string, handled map[string]bool, judge func(runtime string) (BaseAdoption, error)) (ReleaseBasesResult, error) {
	var result ReleaseBasesResult
	for _, runtime := range runtimes {
		if handled[runtime] {
			continue
		}
		adoption, err := judge(runtime)
		if err != nil {
			return ReleaseBasesResult{}, fmt.Errorf("imaged: judge release base %s: %w", releaseBaseName(runtime), err)
		}
		switch adoption.Verdict {
		case BaseAdoptionCached, BaseAdoptionPublished:
			result.Current = append(result.Current, runtime)
		case BaseAdoptionUnpublished:
			result.Unpublished = append(result.Unpublished, runtime)
		default:
			result.Staged = append(result.Staged, runtime)
		}
	}
	return result, nil
}

// stageReleaseBases stages each runtime in order, with the assigned pass's
// deferral rule: only a pre-stage may defer, and only a child base whose
// verified parent the serving vmmd cannot materialize.
func stageReleaseBases(runtimes []string, deferLegacyParent bool, stage func(string) error) ([]string, error) {
	var deferred []string
	for _, runtime := range runtimes {
		err := stage(runtime)
		if err != nil && deferLegacyParent && errors.Is(err, ErrVerifiedParentUnsupported) {
			deferred = append(deferred, runtime)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("imaged: publish release base %s: %w", releaseBaseName(runtime), err)
		}
	}
	return deferred, nil
}

func releaseBaseName(runtime string) string {
	if runtime == "" {
		return "minimal"
	}
	return runtime
}
