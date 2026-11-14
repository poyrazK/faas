package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A scan that newly finds a fixable CRITICAL in a base apps already serve on
// must not stop them waking at once (deploy-path review P5): vmmd admits the
// boot inside api.BaseScanCriticalBootGrace and flags it so schedd can refuse
// a new deployment's prime. Outside the grace, without a start, or with a
// start in the future, the CRITICAL refuses as before.
func TestBringUpScanCheck_FixableCriticalGrace(t *testing.T) {
	const baseKey = "base/runner-node22-amd64.ext4"
	scanKey := wire.ScanKeyForBaseKey(baseKey)
	now := time.Now().UTC()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, tc := range []struct {
		name      string
		since     *time.Time
		critical  int
		wantGrace bool
		wantErr   bool
	}{
		{name: "inside grace admits with flag", since: at(-time.Hour), critical: 1, wantGrace: true},
		{name: "grace ended refuses", since: at(-api.BaseScanCriticalBootGrace - time.Minute), critical: 1, wantErr: true},
		{name: "no grace start refuses", critical: 1, wantErr: true},
		{name: "future grace start refuses", since: at(time.Hour), critical: 1, wantErr: true},
		{name: "scanner failure never gets grace", since: at(-time.Hour), critical: 9999, wantErr: true},
		{name: "clean base has no flag", since: at(-time.Hour), critical: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sidecar := map[string]any{
				"image":                  "ghcr.io/onebox-faas/runner-node22:latest",
				"findings":               map[string]int{"CRITICAL": tc.critical},
				"fix_available_findings": map[string]int{"CRITICAL": tc.critical},
			}
			if tc.since != nil {
				sidecar["fixable_critical_since"] = tc.since
			}
			blob, err := json.Marshal(sidecar)
			if err != nil {
				t.Fatal(err)
			}
			m := &Manager{}
			m.WithStorage(&memStorage{blobs: map[string][]byte{scanKey: blob}})
			grace, err := m.bringUpScanCheck(context.Background(), baseKey)
			if tc.wantErr {
				var p *api.Problem
				if !errors.As(err, &p) || p.Code != api.CodeScanCritical {
					t.Fatalf("bringUpScanCheck = (%t, %v), want a scan_critical refusal", grace, err)
				}
				return
			}
			if err != nil || grace != tc.wantGrace {
				t.Fatalf("bringUpScanCheck = (%t, %v), want (%t, nil)", grace, err, tc.wantGrace)
			}
		})
	}
}
