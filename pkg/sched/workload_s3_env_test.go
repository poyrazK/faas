// adr: 20261122191423 — apps with a workload bucket grant get the S3 endpoint and region.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEngineCompletesWorkloadS3Env(t *testing.T) {
	granted := func(extra map[string]string) state.App {
		env := map[string]string{api.WorkloadCredentialsEnv: api.WorkloadObjectCredentialsEndpoint}
		for k, v := range extra {
			env[k] = v
		}
		return state.App{Manifest: state.AppManifest{Env: env}}
	}
	lookup := func(env []fcvm.APIEnvEntry) map[string]string {
		out := map[string]string{}
		for _, e := range env {
			out[e.Key] = e.Value
		}
		return out
	}
	customer := state.App{Manifest: state.AppManifest{Env: map[string]string{api.WorkloadCredentialsEnv: "http://169.254.170.2/creds"}}}
	for name, tc := range map[string]struct {
		engine *sched.Engine
		app    state.App
		want   map[string]string
	}{
		"no grant":          {new(sched.Engine), state.App{}, map[string]string{}},
		"customer endpoint": {new(sched.Engine), customer, map[string]string{}},
		"defaults": {new(sched.Engine), granted(nil),
			map[string]string{api.WorkloadS3EndpointEnv: api.DefaultObjectStoragePublicEndpoint, "AWS_REGION": api.DefaultObjectStoragePublicRegion}},
		"configured": {new(sched.Engine).WithWorkloadS3(" https://s3.example.test/ ", "eu-1"), granted(nil),
			map[string]string{api.WorkloadS3EndpointEnv: "https://s3.example.test", "AWS_REGION": "eu-1"}},
		"app sets its own": {new(sched.Engine), granted(map[string]string{api.WorkloadS3EndpointEnv: "https://minio.local", "AWS_DEFAULT_REGION": "x"}),
			map[string]string{}},
	} {
		got := lookup(tc.engine.XWithWorkloadS3Env(tc.app, nil))
		if len(got) != len(tc.want) {
			t.Errorf("%s: env = %v, want %v", name, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got[k], v)
			}
		}
	}
}
