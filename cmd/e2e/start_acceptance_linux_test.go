//go:build linux

package e2e_test

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type startAcceptanceFixture struct {
	platform    *normalPathFixture
	bin         string
	source      string
	stateDir    string
	environment string
	private     bool
	cliEnv      []string
}

func newStartAcceptanceFixture(t *testing.T, bin, slug string, private bool, environment string, extraEnv ...string) *startAcceptanceFixture {
	t.Helper()
	f := newNormalPathFixtureWithRequest(t, slug, api.PlanPro, 0, api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &private,
	}, extraEnv...)
	if f == nil {
		t.Fatal("start acceptance requires PostgreSQL; use make e2e-start")
	}
	// Pro defaults both legacy and public-auth gates independently. Make the
	// public fixture explicitly open so it exercises TLS and the public edge.
	if !private {
		raw, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
			map[string]any{"public_auth": map[string]string{"mode": api.AppPublicAuthModeOpen}})
		if status != http.StatusOK {
			t.Fatalf("configure public access: status=%d body=%s", status, raw)
		}
	}
	root := t.TempDir()
	source := filepath.Join(root, slug)
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := templates.Materialize("hello-node", source); err != nil {
		t.Fatal(err)
	}
	if environment != "" {
		app, err := f.store.AppByID(f.ctx, f.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		project, err := f.store.CreateProject(f.ctx, state.Project{
			AccountID: app.AccountID, Slug: slug + "-project", ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention,
		})
		if err != nil {
			t.Fatal(err)
		}
		// The standalone seed app remains untouched. This project owns a
		// separate workload, matching a customer's existing linked service.
		linked, err := f.store.CreateApp(f.ctx, state.App{
			AccountID: app.AccountID, ProjectID: project.ID, Slug: slug + "-linked", WorkloadName: "web",
			Type: state.AppTypeApp, Status: state.AppActive, RAMMB: app.RAMMB,
			MaxConcurrency: app.MaxConcurrency, RequireAuthn: private, PublicAuthMode: api.AppPublicAuthModeOpen,
			NodeID: f.nodeID,
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/projects/"+project.Slug+"/environments",
			api.CreateProjectEnvironmentRequest{Slug: environment}, map[string]string{"Idempotency-Key": slug})
		if status != http.StatusCreated {
			t.Fatalf("create environment: status=%d body=%s", status, raw)
		}
		raw, status = doReq(t, f.h, f.key, http.MethodGet, "/v1/apps/"+linked.Slug, nil)
		if status != http.StatusOK || json.Unmarshal(raw, &f.app) != nil {
			t.Fatalf("read linked app: status=%d body=%s", status, raw)
		}
		link, _ := json.Marshal(map[string]any{"version": 1, "project": project.Slug, "app": linked.Slug, "environment": environment})
		writeStartAcceptanceFile(t, filepath.Join(source, ".gregale", "project.json"), string(link))
	}
	f.vmmd.SetStrictInstances(true)
	proxy, ca := startAcceptanceTLS(t, f.h.EdgeURL())
	stateDir := filepath.Join(root, "state")
	fixture := &startAcceptanceFixture{platform: f, bin: bin, source: source, stateDir: stateDir, environment: environment, private: private,
		cliEnv: []string{
			"PATH=" + os.Getenv("PATH"), "TERM=dumb", "NO_COLOR=1",
			"FAAS_API=" + f.h.APIDURL, "FAAS_TOKEN=" + f.key,
			"XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_STATE_HOME=" + stateDir,
			"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
			"HTTPS_PROXY=" + proxy, "NO_PROXY=localhost,127.0.0.1", "SSL_CERT_FILE=" + ca,
		},
	}
	return fixture
}

func writeStartAcceptanceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *startAcceptanceFixture) start(t *testing.T) *startTerminal {
	t.Helper()
	return newStartTerminal(t, f.bin, f.source, f.platform.key, f.cliEnv)
}

func (f *startAcceptanceFixture) selectSource(s *startTerminal) {
	s.t.Helper()
	s.answer("Choose [1]: ", "1")
	if f.environment == "" {
		s.expect("already exists in this account.")
		s.answer("Choose [1]: ", "2")
	}
}

func (f *startAcceptanceFixture) accepted(t *testing.T, previous ...string) state.Deployment {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := f.platform.store.ListDeploymentsForApp(t.Context(), f.platform.app.ID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if !slices.Contains(previous, row.ID) {
				// A deployment row becomes visible before its queued build and
				// the CLI's acceptance callback. Wait for the durable receipt.
				build, err := f.platform.store.BuildByDeployment(t.Context(), row.ID)
				if err != nil && !errors.Is(err, state.ErrNotFound) {
					t.Fatal(err)
				}
				if build.ID == "" || !f.sessionAccepted(row.ID) {
					continue
				}
				if row.Scope != f.scope() || row.SourcePath == "" || row.SourceSHA256 == "" {
					t.Fatalf("accepted deployment has wrong scope or lacks uploaded source: id=%s scope=%s", row.ID, row.Scope)
				}
				return row
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("CLI did not submit a source deployment")
	return state.Deployment{}
}

func (f *startAcceptanceFixture) sessionAccepted(id string) bool {
	paths, err := filepath.Glob(filepath.Join(f.stateDir, "gregale", "start", "*.json"))
	if err != nil || len(paths) != 1 {
		return false
	}
	data, err := os.ReadFile(paths[0])
	var saved struct {
		DeploymentID string `json:"deployment_id"`
	}
	return err == nil && json.Unmarshal(data, &saved) == nil && saved.DeploymentID == id
}

func (f *startAcceptanceFixture) scope() string {
	if f.environment == "" {
		return "default"
	}
	return f.environment
}

// Finish the real API's accepted build at the same state boundary used by the
// normal-path suite. No builder runs here. The first GET must wake a new
// instance through the real scheduler, then forward through the VMMD protocol.
func (f *startAcceptanceFixture) publish(t *testing.T, dep state.Deployment, version string) {
	t.Helper()
	p := f.platform
	build, err := p.store.BuildByDeployment(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.ClaimQueuedBuild(t.Context(), build.ID); err != nil {
		t.Fatal(err)
	}
	publishNormalPathLayer(t, p, dep.ID)
	if err := p.store.UpdateBuildStatus(t.Context(), build.ID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	current, err := p.store.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stages state.StageState
	if err := json.Unmarshal(current.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	index := slices.Index(state.AllStageNames, stages.Current)
	if index < 0 {
		t.Fatalf("unknown fixture deployment stage %q", stages.Current)
	}
	for i := index + 1; i < len(state.AllStageNames); i++ {
		if _, err := p.store.AppendDeploymentStage(t.Context(), dep.ID, state.AllStageNames[i-1], state.AllStageNames[i], time.Now(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.store.CloseDeploymentStage(t.Context(), dep.ID, state.StageReadiness, time.Now()); err != nil {
		t.Fatal(err)
	}
	p.vmmd.SetDefaultVersion(version)
	if err := p.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	notifyNormalPathDeploymentChanged(t, p, dep.ID)
}

func (f *startAcceptanceFixture) assertResponse(t *testing.T, dep state.Deployment, version string, s *startTerminal) {
	t.Helper()
	s.expect("Keep building in this project.")
	if !strings.Contains(s.transcript(), "normal-path:"+version) {
		t.Fatalf("CLI did not show deployment response %q\n%s", version, s.transcript())
	}
	wantOwner := f.private || strings.Contains(s.transcript(), "Checking the selected deployment through your account")
	if ownerCheck := strings.Contains(s.transcript(), "owner-authorized check"); ownerCheck != wantOwner {
		t.Fatalf("response check used the wrong access path: private=%t owner-check=%t", f.private, ownerCheck)
	}
	request := f.platform.vmmd.LastRequest()
	if request == nil {
		t.Fatal("CLI check did not reach VMMD")
	}
	instance, err := f.platform.store.InstanceByID(t.Context(), request.Instance)
	if err != nil || instance.DeploymentID != dep.ID || instance.AppID != dep.AppID {
		t.Fatalf("CLI checked another deployment: instance=%s deployment=%s want=%s err=%v", instance.ID, instance.DeploymentID, dep.ID, err)
	}
	for _, header := range request.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			t.Fatal("authorization credential reached the guest")
		}
	}
	woke := false
	for _, call := range f.platform.vmmd.ColdBootCalls() {
		if call.GetInstance() == instance.ID {
			woke = true
		}
	}
	if !woke {
		t.Fatal("response did not exercise a cold wake of the selected deployment")
	}
	f.assertSession(t, dep.ID, dep.ID)
}

func (f *startAcceptanceFixture) assertSession(t *testing.T, accepted, verified string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(f.stateDir, "gregale", "start", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("session files=%d err=%v", len(paths), err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		AppID                string `json:"app_id"`
		DeploymentID         string `json:"deployment_id"`
		VerifiedDeploymentID string `json:"verified_deployment_id"`
		Environment          string `json:"environment"`
		RequireEvidence      bool   `json:"require_deployment_evidence"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AppID != f.platform.app.ID || saved.DeploymentID != accepted || saved.VerifiedDeploymentID != verified || saved.Environment != f.environment || !saved.RequireEvidence {
		t.Fatalf("session target/recovery IDs differ: %+v", saved)
	}
	if strings.Contains(string(data), f.platform.key) || strings.Contains(string(data), "normal-path:") || strings.Contains(string(data), "acceptance-marker") {
		t.Fatal("session persisted credentials, response data, or source contents")
	}
	info, err := os.Stat(paths[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("session permissions: info=%v err=%v", info, err)
	}
}

func assertStartUploadedFile(t *testing.T, dep state.Deployment, name, want string) {
	t.Helper()
	file, err := os.Open(dep.SourcePath) //nolint:forbidigo // Spool path from this isolated harness's accepted deployment, never customer input.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimPrefix(header.Name, "./") == name {
			data, err := io.ReadAll(io.LimitReader(archive, 4096))
			if err != nil || string(data) != want {
				t.Fatalf("uploaded %s differs from reviewed source: err=%v", name, err)
			}
			return
		}
	}
	t.Fatalf("reviewed file %s is absent from accepted upload", name)
}

func TestE2E_Start_LaunchEditRedeploy(t *testing.T) {
	bin := buildGregale(t)
	for _, tc := range []struct {
		name, environment string
		private           bool
		internal          bool
	}{
		{name: "public-default"}, {name: "public-staging", environment: "staging"},
		{name: "private-default", private: true}, {name: "private-staging", private: true, environment: "staging"},
		{name: "internal-default", internal: true}, {name: "internal-staging", internal: true, environment: "staging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStartAcceptanceFixture(t, bin, "start-"+tc.name, tc.private, tc.environment)
			if tc.internal {
				raw, status := doReq(t, f.platform.h, f.platform.key, http.MethodPatch, "/v1/apps/"+f.platform.app.Slug,
					map[string]string{"visibility": string(api.AppVisibilityInternal)})
				if status != http.StatusOK {
					t.Fatalf("configure internal visibility: status=%d body=%s", status, raw)
				}
				f.private = true
			}
			s := f.start(t)
			f.selectSource(s)
			s.answer("Deploy this app (y/N) [n]: ", "y")
			first := f.accepted(t, "")
			f.publish(t, first, "v1")
			if !f.private {
				s.expect("The gateway answered this health check")
				s.expect("Your deployment is saved. Let's check its response.")
				s.answer("Choose [1]: ", "2")
				s.answer("GET path [/healthz]: ", "/")
			}
			f.assertResponse(t, first, "v1", s)
			writeStartAcceptanceFile(t, filepath.Join(f.source, "revision.txt"), "acceptance-marker-v2\n")
			s.answer("Choose [1]: ", "2")
			s.expect(`added    "revision.txt"`)
			s.answer("Deploy this app (y/N) [n]: ", "y")
			second := f.accepted(t, first.ID)
			if second.SourceSHA256 == first.SourceSHA256 {
				t.Fatal("redeploy reused the original source archive")
			}
			assertStartUploadedFile(t, second, filepath.Base(f.source)+"/revision.txt", "acceptance-marker-v2\n")
			f.publish(t, second, "v2")
			f.assertResponse(t, second, "v2", s)
			s.answer("Choose [1]: ", "1")
			s.wait(0)
			t.Logf("verified app=%s scope=%s deployments=%s,%s", f.platform.app.ID, f.scope(), first.ID, second.ID)
		})
	}
}

func TestE2E_Start_InterruptedFirstLaunchResumesExactDeployment(t *testing.T) {
	bin := buildGregale(t)
	f := newStartAcceptanceFixture(t, bin, "start-first-resume", false, "")
	s := f.start(t)
	f.selectSource(s)
	s.answer("Deploy this app (y/N) [n]: ", "y")
	dep := f.accepted(t)
	s.expect("Waiting for a builder")
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	s.wait(130)
	f.assertSession(t, dep.ID, "")
	f.publish(t, dep, "first-resumed")
	s = f.start(t)
	s.expect("Welcome back.")
	s.answer("Choose [1]: ", "1")
	s.expect("The gateway answered this health check")
	s.answer("Choose [1]: ", "2")
	s.answer("GET path [/healthz]: ", "/")
	f.assertResponse(t, dep, "first-resumed", s)
	if !strings.Contains(s.transcript(), "owner-authorized check") || strings.Contains(s.transcript(), "Verified URL") {
		t.Fatal("resumed first launch claimed public deployment evidence without an owner check")
	}
	s.answer("Choose [1]: ", "1")
	s.wait(0)
	rows, err := f.platform.store.ListDeploymentsForApp(t.Context(), f.platform.app.ID, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("first-launch resume submitted another deployment: count=%d err=%v", len(rows), err)
	}
}

func TestE2E_Start_ResponseFailureAndRetryPreserveDeployment(t *testing.T) {
	bin := buildGregale(t)
	f := newStartAcceptanceFixture(t, bin, "start-response-retry", true, "")
	s := f.start(t)
	f.selectSource(s)
	s.answer("Deploy this app (y/N) [n]: ", "y")
	dep := f.accepted(t)
	f.publish(t, dep, "v1")
	f.assertResponse(t, dep, "v1", s)
	s.answer("Choose [1]: ", "1")
	s.wait(0)
	instance := f.platform.vmmd.LastRequest().Instance
	f.platform.vmmd.SetResponse(instance, e2etest.FakeResponse{Status: http.StatusServiceUnavailable, Body: []byte("fixture unavailable")})
	s = f.start(t)
	s.expect("Welcome back.")
	s.answer("Choose [1]: ", "1")
	s.expect("Your deployment is saved. Let's check its response.")
	s.answer("Choose [1]: ", "4")
	s.wait(1)
	f.assertSession(t, dep.ID, dep.ID)
	s = f.start(t)
	s.expect("Welcome back.")
	s.answer("Choose [1]: ", "1")
	s.expect("Your deployment is saved. Let's check its response.")
	f.platform.vmmd.SetResponse(instance, e2etest.FakeResponse{
		Status: http.StatusOK, Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}}, Body: []byte("normal-path:recovered\n"),
	})
	s.answer("Choose [1]: ", "1")
	f.assertResponse(t, dep, "recovered", s)
	s.answer("Choose [1]: ", "1")
	s.wait(0)
	rows, err := f.platform.store.ListDeploymentsForApp(t.Context(), f.platform.app.ID, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("GET recovery submitted another deployment: count=%d err=%v", len(rows), err)
	}
}

func TestE2E_Start_FailedBuildAndInterruptedUpdateResume(t *testing.T) {
	bin := buildGregale(t)
	f := newStartAcceptanceFixture(t, bin, "start-recovery", true, "staging")
	s := f.start(t)
	f.selectSource(s)
	s.answer("Deploy this app (y/N) [n]: ", "y")
	first := f.accepted(t, "")
	f.publish(t, first, "v1")
	f.assertResponse(t, first, "v1", s)
	s.answer("Choose [1]: ", "2")
	s.answer("Deploy this app (y/N) [n]: ", "y")
	failed := f.accepted(t, first.ID)
	build, err := f.platform.store.BuildByDeployment(t.Context(), failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.platform.store.ClaimQueuedBuild(t.Context(), build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.platform.store.FailBuild(t.Context(), claim, state.FailureUserError, "acceptance build failure"); err != nil {
		t.Fatal(err)
	}
	s.expect("The deployment failed. Your source and deployment ID are saved.")
	f.assertSession(t, failed.ID, first.ID)
	s.answer("Choose [1]: ", "3")
	s.wait(1)
	s = f.start(t)
	s.expect("Welcome back.")
	s.answer("Choose [1]: ", "1")
	s.expect("The deployment failed. Your source and deployment ID are saved.")
	s.answer("Choose [1]: ", "1")
	s.answer("Deploy this app (y/N) [n]: ", "y")
	pending := f.accepted(t, first.ID, failed.ID)
	s.expect("Waiting for a builder") // onQueued has durably saved its ID.
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	s.wait(130)
	f.assertSession(t, pending.ID, first.ID)
	f.publish(t, pending, "recovered")
	s = f.start(t)
	s.expect("Welcome back.")
	s.answer("Choose [1]: ", "1")
	f.assertResponse(t, pending, "recovered", s)
	s.answer("Choose [1]: ", "1")
	s.wait(0)
	rows, err := f.platform.store.ListDeploymentsForApp(t.Context(), f.platform.app.ID, 10, 0)
	if err != nil || len(rows) != 3 {
		t.Fatalf("resume submitted another deployment: count=%d err=%v", len(rows), err)
	}
	t.Logf("recovered app=%s scope=%s failed=%s resumed=%s", f.platform.app.ID, f.scope(), failed.ID, pending.ID)
}

func TestE2E_Start_MissingConfigurationCanBeSupplied(t *testing.T) {
	bin := buildGregale(t)
	recipient := filepath.Join(t.TempDir(), "host.age.pub")
	if err := writeTestRecipient(recipient); err != nil {
		t.Fatal(err)
	}
	f := newStartAcceptanceFixture(t, bin, "start-config", true, "staging",
		"FAAS_HOST_AGE_RECIPIENT_PATH="+recipient, "FAAS_HOST_AGE_IDENTITY_PATH="+recipient+".priv")
	path := filepath.Join(f.source, "handler.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeStartAcceptanceFile(t, path, string(data)+"\nconsole.log(process.env.START_ACCEPTANCE_REQUIRED);\n")
	s := f.start(t)
	f.selectSource(s)
	s.expect("Required runtime keys need configuration: START_ACCEPTANCE_REQUIRED")
	s.expect("Let's resolve the source blockers before deploying.")
	rows, err := f.platform.store.ListDeploymentsForApp(t.Context(), f.platform.app.ID, 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing configuration submitted a deployment: count=%d err=%v", len(rows), err)
	}
	secretsPath := filepath.Join(f.source, "acceptance-keys.txt")
	const fixtureValue = "acceptance-only-value"
	writeStartAcceptanceFile(t, secretsPath, "START_ACCEPTANCE_REQUIRED="+fixtureValue+"\n")
	s.answer("Choose [4]: ", "4")
	s.answer("Secrets file path: ", secretsPath)
	s.answer("Deploy this app (y/N) [n]: ", "y")
	dep := f.accepted(t, "")
	for _, scope := range []string{"default", "staging"} {
		raw, status := doReq(t, f.platform.h, f.platform.key, http.MethodGet, "/v1/apps/"+f.platform.app.Slug+"/secrets?scope="+scope, nil)
		var metadata api.AppSecretListResponse
		if status != http.StatusOK || json.Unmarshal(raw, &metadata) != nil {
			t.Fatalf("read secret metadata: status=%d", status)
		}
		if want := map[string]int{"default": 0, "staging": 1}[scope]; len(metadata.Secrets) != want || strings.Contains(string(raw), fixtureValue) {
			t.Fatalf("secret scope or redaction differs: scope=%s count=%d", scope, len(metadata.Secrets))
		}
	}
	assertStartUploadExcludes(t, dep, "acceptance-keys.txt", fixtureValue)
	f.publish(t, dep, "configured")
	f.assertResponse(t, dep, "configured", s)
	s.answer("Choose [1]: ", "1")
	s.wait(0)
	paths, err := filepath.Glob(filepath.Join(f.stateDir, "gregale", "start", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range paths {
		content, err := os.ReadFile(session)
		if err != nil || strings.Contains(string(content), fixtureValue) || strings.Contains(string(content), secretsPath) {
			t.Fatalf("secret value or path persisted in session: err=%v", err)
		}
	}
	if strings.Contains(s.transcript(), fixtureValue) {
		t.Fatal("secret value appeared in the CLI transcript")
	}
	t.Logf("configuration recheck verified deployment=%s", dep.ID)
}

func assertStartUploadExcludes(t *testing.T, dep state.Deployment, filename, value string) {
	t.Helper()
	file, err := os.Open(dep.SourcePath) //nolint:forbidigo // Accepted source spool owned by this isolated test harness.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(archive, 64<<10))
		if err != nil || filepath.Base(header.Name) == filename || strings.Contains(string(data), value) {
			t.Fatalf("secret file or value reached source upload: err=%v", err)
		}
	}
}
