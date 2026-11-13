// adr: 053, 191, 20261024100000 — runtime port handoff and bounded recovery.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestSourceBuildRuntimePortMatchesScheduler(t *testing.T) {
	for _, tc := range []struct {
		name, dockerfile         string
		port, overridePort       int
		noProfile, nullProfile   bool
		healthcheck, failProfile bool
	}{
		{name: "explicit-8080-control", dockerfile: "FROM example.test/web-base:latest\nEXPOSE 8080\nCOPY . /app\n", port: 8080},
		{name: "inherited-3000", dockerfile: "FROM example.test/web-base:latest\nCOPY . /app\n", port: 3000},
		{name: "inherited-80", dockerfile: "FROM nginx:alpine\nCOPY dist /usr/share/nginx/html\n", port: 80},
		{name: "explicit-port-override", dockerfile: "FROM example.test/web-base:latest\n", port: 3000, overridePort: 9090},
		{name: "missing-inference", dockerfile: "FROM example.test/web-base:latest\n", port: 3000, noProfile: true},
		{name: "null-inference", dockerfile: "FROM example.test/web-base:latest\n", port: 3000, nullProfile: true},
		{name: "port-and-healthcheck", dockerfile: "FROM example.test/web-base:latest\n", port: 3000, healthcheck: true},
		{name: "profile-write-failure", dockerfile: "FROM example.test/web-base:latest\n", port: 3000, failProfile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "source-port@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "source-port", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			inferred, err := frameworkprofile.Analyze(fstest.MapFS{"Dockerfile": {Data: []byte(tc.dockerfile)}})
			if err != nil {
				t.Fatal(err)
			}
			// apidsource.Enqueue makes Dockerfile selection authoritative while
			// preserving the source analyzer's numeric port.
			inferred.Framework = "docker"
			inferred.FrameworkVer, inferred.PackageManager, inferred.StartCommand = "", "", ""
			inferred.Inferred = false
			profile, err := json.Marshal(inferred)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(profile, &fields); err != nil {
				t.Fatal(err)
			}
			fields["future_field"] = json.RawMessage(`{"keep":true}`)
			profile, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if tc.noProfile {
				profile = nil
			} else if tc.nullProfile {
				profile = []byte(`null`)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging, InferredProfile: profile, OverridePort: tc.overridePort})
			if err != nil {
				t.Fatal(err)
			}
			rawLayer := []byte("built source image")
			var config map[string]any
			if err := json.Unmarshal(minimalConfigBytes(rawLayer), &config); err != nil {
				t.Fatal(err)
			}
			process := map[string]any{"Cmd": []string{"/app/serve"}, "ExposedPorts": map[string]any{fmt.Sprintf("%d/tcp", tc.port): map[string]any{}}}
			if tc.healthcheck {
				process["Healthcheck"] = map[string]any{"Test": []string{"CMD", "/app/check"}}
			}
			config["config"] = process
			configBytes, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			layer := gzipBytes(t, rawLayer)
			manifest := minimalManifestBytes(configBytes, layer)
			archive := buildLocalOCIArchive(t, map[string][]byte{"index.json": minimalIndexBytes(manifest), blobPath(digestFor(t, manifest)): manifest, blobPath(digestFor(t, configBytes)): configBytes, blobPath(digestFor(t, layer)): layer})
			if err := store.SetDeploymentRootfs(ctx, dep.ID, archive, "builder/source-port", 1); err != nil {
				t.Fatal(err)
			}
			dep, err = store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			builder := &fakeBuilder{bytesOut: 128}
			var handlerStore state.Store = store
			if tc.failProfile {
				handlerStore = statetest.NewMem(&sourceRuntimeProfileFailureStore{MemStore: store})
			}
			h := New(handlerStore, &fakeNotifier{}, nil, builder, "", t.TempDir(), silentLogger())
			err = h.buildLocalOCIAppLayer(ctx, app, &dep, acct, nil)
			if tc.failProfile {
				if !errors.Is(err, errSourceRuntimeProfileWrite) || len(builder.calls) != 0 {
					t.Fatalf("profile failure must stop layer publication: err=%v builds=%d", err, len(builder.calls))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			guestPort := builder.calls[0].Manifest.Port
			routingPort := sched.DeploymentRuntimePort(stored)
			t.Logf("source Dockerfile=%q built image advertised port=%d guest manifest port=%d scheduler DNAT port=%d persisted profile=%s", tc.dockerfile, tc.port, guestPort, routingPort, stored.InferredProfile)
			wantPort := tc.port
			if tc.overridePort != 0 {
				wantPort = tc.overridePort
			}
			if guestPort != wantPort {
				t.Fatalf("guest port = %d; want %d", guestPort, wantPort)
			}
			if routingPort != guestPort {
				t.Errorf("readiness/routing target port %d does not reach guest port %d", routingPort, guestPort)
			}
			if !tc.noProfile && !tc.nullProfile {
				var recorded map[string]json.RawMessage
				if err := json.Unmarshal(stored.InferredProfile, &recorded); err != nil || string(recorded["future_field"]) != `{"keep":true}` {
					t.Fatalf("runtime handoff lost unknown fields: %s (%v)", stored.InferredProfile, err)
				}
			}
			if required := frameworkprofile.RequiresSourceImageHealthcheck(stored.InferredProfile); required != tc.healthcheck {
				t.Fatalf("image healthcheck requirement = %v; want %v", required, tc.healthcheck)
			}
		})
	}
}

var errSourceRuntimeProfileWrite = errors.New("runtime profile write unavailable")

type sourceRuntimeProfileFailureStore struct {
	MemStore *state.MemStore
}

func (*sourceRuntimeProfileFailureStore) SetDeploymentRuntimeProfile(context.Context, string, []byte) error {
	return errSourceRuntimeProfileWrite
}

func TestReconcileStaleDeploymentsProgressesPastActiveBuilds(t *testing.T) {
	for _, blockers := range []int{0, 64, 128} {
		t.Run(fmt.Sprint(blockers), func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			now := time.Now().UTC()
			seed := func(i int, age time.Duration) state.Deployment {
				acct, err := store.CreateAccount(ctx, fmt.Sprintf("stale-%d@example.test", i), api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("stale-%d", i), Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding, CreatedAt: now.Add(-age)})
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			var active []state.Deployment
			for i := 0; i < blockers; i++ {
				dep := seed(i, 4*time.Hour)
				if _, err := store.CreateBuild(ctx, dep.ID, dep.Kind, 1, ""); err != nil {
					t.Fatal(err)
				}
				active = append(active, dep)
			}
			target := seed(blockers, 3*time.Hour)
			loop := NewLoop(LoopConfig{Store: store, Handler: &Handler{ops: wire.NewOpsMetrics("recovery_test")}, Log: silentLogger(), Now: func() time.Time { return now }})
			loop.reconcileStaleDeployments(ctx)
			if blockers >= staleDeploymentBatchSize {
				got, err := store.DeploymentByID(ctx, target.ID)
				if err != nil || got.Status != state.DeployBuilding {
					t.Fatalf("first sweep exceeded its batch budget: %+v (%v)", got, err)
				}
			}
			for i := 0; i < 5; i++ {
				loop.reconcileStaleDeployments(ctx)
			}
			got, err := store.DeploymentByID(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != state.DeployCancelled {
				t.Errorf("eligible stale orphan is never reached behind older active rows")
			}
			for _, dep := range active {
				got, err := store.DeploymentByID(ctx, dep.ID)
				if err != nil || got.Status != state.DeployBuilding {
					t.Fatalf("active build was cancelled: %+v (%v)", got, err)
				}
			}
			if len(active) == 0 {
				return
			}
			// Once an older build finishes, wrapping must revisit its orphan.
			build, err := store.BuildByDeployment(ctx, active[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateBuildStatus(ctx, build.ID, state.BuildRunning, "", true, false); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateBuildStatus(ctx, build.ID, state.BuildFailed, "", false, true); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				loop.reconcileStaleDeployments(ctx)
			}
			got, err = store.DeploymentByID(ctx, active[0].ID)
			if err != nil || got.Status != state.DeployCancelled {
				t.Fatalf("wrapped sweep did not revisit older orphan: %+v (%v)", got, err)
			}
		})
	}
}
