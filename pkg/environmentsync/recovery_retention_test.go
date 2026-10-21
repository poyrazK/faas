package environmentsync

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestCompileRecoveryRetentionIsExplicitBoundedDigestIntent(t *testing.T) {
	d := api.EnvironmentDefinition{APIVersion: APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{}}
	plain, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	p := api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: 1000}
	d.RecoveryRetention = &p
	retained, err := Compile(d)
	if err != nil || retained.Digest == plain.Digest {
		t.Fatal("policy missing from review digest", err)
	}
	for _, bad := range []api.EnvironmentRecoveryRetention{{}, {Releases: 6, Seconds: 1, MaxBytes: 1}, {Releases: 1, Seconds: api.EnvironmentRecoveryMaxSeconds + 1, MaxBytes: 1}, {Releases: 1, Seconds: 1, MaxBytes: api.EnvironmentRecoveryMaxBytes + 1}} {
		d.RecoveryRetention = &bad
		if _, err := Compile(d); err == nil {
			t.Fatal("invalid policy accepted", bad)
		}
	}
}
