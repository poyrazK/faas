// adr: 375
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentSmokeCandidateRejectsIdentityBeforeDatabase(t *testing.T) {
	store := &state.PgStore{}
	valid := "00000000-0000-0000-0000-00000000000a"
	for _, tc := range []struct{ name, app, deployment string }{
		{"empty app", "", valid}, {"empty deployment", valid, ""},
		{"invalid app", "bad", valid}, {"invalid deployment", valid, "bad"},
		{"noncanonical app", "00000000-0000-0000-0000-00000000000A", valid},
		{"noncanonical deployment", valid, "00000000-0000-0000-0000-00000000000A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, found, err := store.RunningDeploymentSmokeTarget(t.Context(), tc.app, tc.deployment)
			if err == nil || found || target.InstanceID != "" {
				t.Fatalf("invalid identity reached database: target=%+v found=%t err=%v", target, found, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, found, err := store.RunningDeploymentSmokeTarget(ctx, valid, valid); found || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup reached database: found=%t err=%v", found, err)
	}
}
