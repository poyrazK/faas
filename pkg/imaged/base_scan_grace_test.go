package imaged

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// The sidecar's fixable_critical_since starts vmmd's boot grace for a base
// with a newly found fixable CRITICAL (deploy-path review P5). A rescan of
// the same generation must keep the clock, or a rescan loop would extend the
// grace forever; a scanner failure's placeholder never earns grace.
func TestFixableCriticalSince(t *testing.T) {
	const baseKey = "base/runner-node22-amd64.ext4"
	const ref = "ghcr.io/onebox-faas/runner-node22@sha256:aa"
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-5 * time.Hour)
	for _, tc := range []struct {
		name  string
		prior string
		n     int
		want  *time.Time
	}{
		{name: "clean base has no start", n: 0},
		{name: "scanner failure placeholder has no start", n: wire.BaseScanFailedCount},
		{name: "first finding starts now", n: 2, want: &now},
		{name: "rescan of the same generation keeps the start", prior: `{"image":"` + ref + `","fixable_critical_since":"` + earlier.Format(time.RFC3339) + `"}`, n: 1, want: &earlier},
		{name: "new generation starts now", prior: `{"image":"ghcr.io/onebox-faas/runner-node22@sha256:bb","fixable_critical_since":"` + earlier.Format(time.RFC3339) + `"}`, n: 1, want: &now},
		{name: "prior clean sidecar starts now", prior: `{"image":"` + ref + `"}`, n: 1, want: &now},
		{name: "future prior start is not trusted", prior: `{"image":"` + ref + `","fixable_critical_since":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}`, n: 1, want: &now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be := newMemoBackend(t)
			if tc.prior != "" {
				if err := be.Put(context.Background(), wire.ScanKeyForBaseKey(baseKey), strings.NewReader(tc.prior)); err != nil {
					t.Fatal(err)
				}
			}
			got := fixableCriticalSince(context.Background(), be, baseKey, ref, tc.n, now)
			if (got == nil) != (tc.want == nil) || (got != nil && !got.Equal(*tc.want)) {
				t.Fatalf("fixableCriticalSince = %v, want %v", got, tc.want)
			}
		})
	}
}
