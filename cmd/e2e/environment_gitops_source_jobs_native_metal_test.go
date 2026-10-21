//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Provider access is a fixture; building, qualification, cron admission,
// guest execution, completion logs and serving evidence use the real daemons.
func TestEnvironmentGitOpsSourceJobsNativeMetal(t *testing.T) {
	if os.Getenv("GREGALE_GITOPS_NATIVE_ACCEPTANCE") != "1" {
		t.Skip("isolated native GitOps acceptance only")
	}
	if !metalAvailable(t) {
		return
	}
	requireNativeGitOpsImageRefs(t)
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			for _, kind := range []string{"source", "dockerfile"} {
				t.Run(kind, func(t *testing.T) { runNativeSourceJob(t, provider, kind) })
			}
		})
	}
}

func requireNativeGitOpsImageRefs(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GREGALE_GITOPS_NATIVE_BUILDER_REF", "GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF", "GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF"} {
		ref, err := oci.ParseReference(os.Getenv(key))
		if err != nil || !strings.HasPrefix(ref.Digest, "sha256:") || len(ref.Digest) != 71 {
			t.Fatalf("%s must be a digest-pinned acceptance image reference", key)
		}
	}
}

type nativeSourceJobJourney struct {
	nativeGitOpsJourney
	kind, jobID string
	binary      []byte
}

func runNativeSourceJob(t *testing.T, providerName, kind string) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	storageRoot := filepath.Join(t.TempDir(), "storage")
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)
	runtimeRef := os.Getenv("GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF")
	if kind == "dockerfile" {
		runtimeRef = os.Getenv("GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF")
	}
	e2etest.ConfigureNativeBaseImages(t, os.Getenv("GREGALE_GITOPS_NATIVE_BUILDER_REF"), runtimeRef,
		filepath.Join(storageRoot, sched.BaseKeyForArch("builder", "amd64")))
	fcVersion, err := fcvm.DetectFirecrackerVersion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stageNativeAcceptanceKernel(t, storageRoot, fcVersion)
	provider, providerEnv := newGitOpsProviderFixture(t, providerName)
	daemonEnv := []string{"FAAS_STORAGE_ROOT=" + storageRoot,
		"GREGALE_GITOPS_ACCEPTANCE=1", "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT=1",
		"FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1",
		"FAAS_AUDIT_HMAC_KEY_FILE=" + gitOpsAcceptanceHMACKeyFile(t, "audit-hmac.key"),
		"FAAS_RECOVERY_HMAC_KEY_FILE=" + gitOpsAcceptanceHMACKeyFile(t, "recovery-hmac.key")}
	h := e2etest.StartWithAPIDEnv(t, pool, e2etest.All, providerEnv, daemonEnv...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(t.Context(), api.PlanPro, "gitops-source-job")
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(t.Context(), "e2e+pro+gitops-source-job@test.example")
	if err != nil {
		t.Fatal(err)
	}
	connectGitOpsProvider(t, provider, store, account.ID)
	project, err := store.CreateProject(t.Context(), gitOpsProviderProject(providerName, "source-jobs", account.ID))
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "scheduled-report",
		WorkloadName: "report", Type: state.AppTypeApp, Status: state.AppActive, WorkloadClass: state.WorkloadClassJob,
		RAMMB: 512, MaxConcurrency: 1, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeJob, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", gitOpsProviderSource(providerName, "enforce"))
	if err != nil {
		t.Fatal(err)
	}
	j := nativeSourceJobJourney{nativeGitOpsJourney: nativeGitOpsJourney{h: h, store: store, client: api.NewClient(h.APIDURL, key),
		key: key, project: project, app: app, source: source, provider: provider}, kind: kind}
	if kind == "dockerfile" {
		j.binary = nativeSourceJobBinary(t)
	}
	j.addSourceRevision(t, strings.Repeat("a", 40), false)
	j.runSourceJob(t)
}

func nativeSourceJobBinary(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate native fixture source")
	}
	out := filepath.Join(t.TempDir(), "gitops-job")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./pkg/e2etest/testdata/gitopsjob")
	cmd.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build checkout-owned Dockerfile executable: %v\n%s", err, output)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (j *nativeSourceJobJourney) addSourceRevision(t *testing.T, sha string, hold bool) {
	t.Helper()
	command := []string{"/bin/sh", "/app/run.sh"}
	files := map[string][]byte{"jobs/report/commit.txt": []byte(sha + "\n")}
	if j.kind == "dockerfile" {
		command = []string{"/gitops-job"}
		files["jobs/report/gitops-job"] = j.binary
		dockerfile := "FROM scratch\nCOPY --chmod=0755 gitops-job /gitops-job\nCOPY commit.txt /app/commit.txt\n"
		if hold {
			dockerfile += "RUN [\"/gitops-job\",\"--build-hold\"]\n"
		}
		files["jobs/report/Dockerfile"] = []byte(dockerfile + "CMD [\"/gitops-job\"]\n")
	} else {
		build := "node index.js --build"
		if hold {
			build = "node index.js --build-hold"
		}
		packageJSON, err := json.Marshal(map[string]any{"name": "gitops-source-job", "version": "1.0.0", "private": true,
			"engines": map[string]string{"node": "22"}, "scripts": map[string]string{"start": "node index.js", "build": build}, "dependencies": map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		files["jobs/report/package.json"] = packageJSON
		files["jobs/report/index.js"] = []byte(nativeSourceJobJS)
		files["jobs/report/run.sh"] = []byte(nativeSourceJobLauncher)
	}
	runtimeJSON, err := json.Marshal(map[string]any{"execution_mode": "job", "entrypoint": command})
	if err != nil {
		t.Fatal(err)
	}
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: j.project.Slug, Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"report": {App: j.app.Slug,
			Source: &api.EnvironmentWorkloadSource{Kind: j.kind, Directory: "jobs/report"}, Runtime: runtimeJSON,
			JobSmoke: &api.EnvironmentJobSmoke{Command: append(append([]string(nil), command...), "--smoke"), TimeoutSeconds: 30},
			Schedule: &api.EnvironmentJobSchedule{Cron: "* * * * *", Timezone: "UTC"}}}}
	j.provider.addFiles(t, sha, definition, files)
}

const nativeSourceJobJS = `const fs = require('fs');
const path = require('path');
const sha = fs.readFileSync(path.join(__dirname, 'commit.txt'), 'utf8').trim();
const mode = process.argv[2];
if (mode === '--build-hold') {
  console.log('gitops-build-hold:' + sha);
  setTimeout(() => {}, 120000);
} else if (mode === '--build' || mode === '--smoke') {
  console.log('gitops-' + mode.slice(2) + ':' + sha);
} else if (!process.env.GREGALE_RUN_ID) {
  console.error('missing scheduled run identity'); process.exit(1);
} else {
  console.log('gitops-run:' + sha + ':' + process.env.GREGALE_RUN_ID);
}
`

// Locate the build image's Node executable without assuming that Railpack's
// installation directory is part of guest-init's default shell PATH.
const nativeSourceJobLauncher = `#!/bin/sh
if command -v node >/dev/null 2>&1; then exec node /app/index.js "$@"; fi
for node in /opt/railpack/node/bin/node /mise/installs/node/*/bin/node /usr/local/bin/node /usr/bin/node; do
  if [ -x "$node" ]; then exec "$node" /app/index.js "$@"; fi
done
echo 'built Node executable missing' >&2
exit 1
`
