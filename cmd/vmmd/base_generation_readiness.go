// adr: 20261022184531 — Ping's base generation report, judged the way the
// activation gate judges a node (ADR-20261024191249).
package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// baseGenerationReadinessCheck judges each configured base with
// imaged.JudgeBaseAdoption, which reads the shared copy without touching this
// node's cache. Only a "stale" verdict is not adoptable. A base it cannot
// judge (storage unreachable) counts as adoptable: readiness needs proof,
// and the wake path refuses on its own evidence.
func baseGenerationReadinessCheck(be storage.StorageBackend, configured map[string]string) vmmdgrpc.BaseGenerationCheck {
	return func(ctx context.Context) []vmmdgrpc.BaseGenerationReadiness {
		return judgeBaseGenerationReadiness(configured, func(key, generation string) (imaged.BaseAdoption, error) {
			return imaged.JudgeBaseAdoption(ctx, be, key, generation)
		})
	}
}

func judgeBaseGenerationReadiness(configured map[string]string, judge func(key, generation string) (imaged.BaseAdoption, error)) []vmmdgrpc.BaseGenerationReadiness {
	keys := make([]string, 0, len(configured))
	for key := range configured {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	report := make([]vmmdgrpc.BaseGenerationReadiness, 0, len(keys))
	for _, key := range keys {
		readiness := vmmdgrpc.BaseGenerationReadiness{BaseKey: key, Generation: configured[key], Adoptable: true}
		if adoption, err := judge(key, configured[key]); err == nil && !adoption.Adoptable() {
			readiness.Adoptable = false
			readiness.Reason = fmt.Sprintf("shared storage publishes %q and this node's cache holds %q", adoption.Published, adoption.Cached)
		}
		report = append(report, readiness)
	}
	return report
}
