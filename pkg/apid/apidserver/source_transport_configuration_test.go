package apidserver_test

// adr: 20261105023000 — source transport configuration survives admission and enqueue.

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func sourceConfigurationArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var headers []tar.Header
	contents := make(map[string][]byte, len(files))
	for name, body := range files {
		headers = append(headers, tar.Header{Name: name})
		contents[name] = []byte(body)
	}
	return apidserver.BuildTestTarGz(t, headers, contents)
}

func TestSourceDeploymentWorkflowDeclarations(t *testing.T) {
	manifest := "workflows:\n  - name: process_order\n    trigger:\n      type: manual\n    steps:\n      - name: charge\n        run: charge_order\n"
	for _, transport := range []string{"source-ref", "multipart", "source-tarball", "resumable"} {
		for _, declaration := range []string{"archive", "request", "request-and-archive"} {
			if declaration != "archive" && transport != "multipart" && transport != "resumable" {
				continue
			}
			t.Run(transport+"/"+declaration, func(t *testing.T) {
				f := newSourceAdmissionFixture(t)
				body, want := manifest, "process_order"
				ann := api.DeployAnnotations{}
				if declaration != "archive" {
					want = "explicit_order"
					ann.Workflows = []api.WorkflowSpec{{Name: want, Steps: []api.WorkflowStepSpec{{Name: "charge", Run: "charge_order"}}}}
				}
				if declaration == "request" {
					body = "schema_version: 1\n"
				}
				response, err := f.deploy(t, transport, sourceAdmissionArchive(t, body), ann)
				if err != nil {
					t.Fatal(err)
				}
				stored, err := (*f.env.Store()).DeploymentByID(t.Context(), response.ID)
				if err != nil {
					t.Fatal(err)
				}
				var workflows []api.WorkflowSpec
				if err := json.Unmarshal(stored.Workflows, &workflows); err != nil {
					t.Fatalf("workflows=%s: %v", stored.Workflows, err)
				}
				if len(workflows) != 1 || workflows[0].Name != want {
					t.Fatalf("workflows=%s; want one definition named %s", stored.Workflows, want)
				}
			})
		}
	}
}

func TestSourceDeploymentConfiguredRoot(t *testing.T) {
	for _, transport := range []string{"source-ref", "multipart", "source-tarball", "resumable"} {
		for _, build := range []string{"railpack", "dockerfile"} {
			t.Run(transport+"/"+build, func(t *testing.T) {
				f := newSourceAdmissionFixture(t)
				store := *f.env.Store()
				root := "apps/api"
				if _, err := store.UpdateApp(t.Context(), *f.env.AppID(), state.UpdateAppParams{RootDir: &root}); err != nil {
					t.Fatal(err)
				}
				files := map[string]string{
					"package.json":              `{"scripts":{"start":"node index.js"}}`,
					"index.js":                  "console.log('root app')\n",
					"data/old.sqlite":           "SQLite format 3\x00",
					"apps/api/requirements.txt": "fastapi==0.116.0\nuvicorn\n",
					"apps/api/main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
					"apps/api/gregale.yaml":     "release:\n  command: python migrate.py\n",
				}
				wantFramework, wantDockerfile := "fastapi", ""
				if build == "dockerfile" {
					files["apps/api/gregale.yaml"] += "build:\n  dockerfile: docker/Dockerfile\n"
					files["apps/api/docker/Dockerfile"] = "FROM scratch\nCOPY . /app\n"
					wantFramework, wantDockerfile = "docker", "docker/Dockerfile"
				}
				response, err := f.deploy(t, transport, sourceConfigurationArchive(t, files), api.DeployAnnotations{})
				if err != nil {
					t.Fatal(err)
				}
				stored, err := store.DeploymentByID(t.Context(), response.ID)
				if err != nil {
					t.Fatal(err)
				}
				var profile frameworkprofile.Profile
				if err := json.Unmarshal(stored.InferredProfile, &profile); err != nil {
					t.Fatal(err)
				}
				if stored.SourceRoot != root || profile.Framework != wantFramework || profile.DockerfilePath != wantDockerfile {
					t.Fatalf("root=%q profile=%+v; want root=%q framework=%q Dockerfile=%q", stored.SourceRoot, profile, root, wantFramework, wantDockerfile)
				}
				if !reflect.DeepEqual(stored.ReleaseCommand, []string{"python migrate.py"}) {
					t.Fatalf("release command=%q; want the selected workload's migration", stored.ReleaseCommand)
				}
			})
		}
	}
}

func TestSourceDeploymentExplicitRootOverridesConfiguredRoot(t *testing.T) {
	for _, transport := range []string{"multipart", "resumable"} {
		for _, requested := range []string{".", "apps/other"} {
			t.Run(transport+"/"+requested, func(t *testing.T) {
				f := newSourceAdmissionFixture(t)
				root := "apps/api"
				if _, err := (*f.env.Store()).UpdateApp(t.Context(), *f.env.AppID(), state.UpdateAppParams{RootDir: &root}); err != nil {
					t.Fatal(err)
				}
				files := map[string]string{
					"package.json":              `{"scripts":{"start":"node index.js"}}`,
					"index.js":                  "console.log('root app')\n",
					"gregale.yaml":              "release:\n  command: node root-migrate.js\n",
					"apps/api/requirements.txt": "fastapi\nuvicorn\n",
					"apps/api/gregale.yaml":     "release:\n  command: python migrate.py\n",
					"apps/other/package.json":   `{"scripts":{"start":"node other.js"}}`,
					"apps/other/gregale.yaml":   "release:\n  command: node other-migrate.js\n",
				}
				raw := sourceConfigurationArchive(t, files)
				var response api.DeploymentResponse
				var err error
				if transport == "multipart" {
					response, err = f.client.DeployMultipartWithSourceRoot(t.Context(), "x", bytes.NewReader(raw), "source.tar.gz", "", "", false, requested, api.DeployAnnotations{})
				} else {
					session, startErr := f.client.StartUpload(t.Context(), "x", int64(len(raw)), "", api.UploadDeployOptions{SourceRoot: requested})
					if startErr != nil {
						t.Fatal(startErr)
					}
					if _, err := f.client.AppendUpload(t.Context(), session.UploadID, 0, raw); err != nil {
						t.Fatal(err)
					}
					response, err = f.client.CommitUpload(t.Context(), session.UploadID)
				}
				if err != nil {
					t.Fatal(err)
				}
				stored, err := (*f.env.Store()).DeploymentByID(t.Context(), response.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantRoot, wantCommand := requested, "node other-migrate.js"
				if requested == "." {
					wantRoot, wantCommand = "", "node root-migrate.js"
				}
				if stored.SourceRoot != wantRoot || !reflect.DeepEqual(stored.ReleaseCommand, []string{wantCommand}) {
					t.Fatalf("root=%q release=%q; want root=%q release=%q", stored.SourceRoot, stored.ReleaseCommand, wantRoot, wantCommand)
				}
			})
		}
	}
}

func TestSourceDeploymentRejectsMissingConfiguredRoot(t *testing.T) {
	for _, transport := range []string{"multipart", "source-tarball", "resumable"} {
		t.Run(transport, func(t *testing.T) {
			f := newSourceAdmissionFixture(t)
			root := "apps/missing"
			if _, err := (*f.env.Store()).UpdateApp(t.Context(), *f.env.AppID(), state.UpdateAppParams{RootDir: &root}); err != nil {
				t.Fatal(err)
			}
			_, err := f.deploy(t, transport, sourceAdmissionArchive(t, "schema_version: 1\n"), api.DeployAnnotations{})
			var remote *api.APIError
			if !errors.As(err, &remote) || remote.Problem.Status != http.StatusBadRequest || remote.Problem.Code != api.CodeSourceInvalid {
				t.Fatalf("missing root error=%v; want source_invalid 400", err)
			}
			if _, err := (*f.env.Store()).LatestDeployment(t.Context(), *f.env.AppID()); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing root enqueued a deployment: %v", err)
			}
		})
	}
}

func TestSourceTarballSDKPreservesSchedule(t *testing.T) {
	for _, reason := range []string{"", "control"} {
		t.Run("reason="+reason, func(t *testing.T) {
			f := newSourceAdmissionFixture(t)
			at := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
			response, err := f.client.DeployFromSourceTarball(t.Context(), "x", bytes.NewReader(sourceAdmissionArchive(t, "schema_version: 1\n")), "source.tar.gz", api.SourceTarballDeployRequest{ReleaseAt: at.Format(time.RFC3339), Reason: reason})
			if err != nil {
				t.Fatal(err)
			}
			store := *f.env.Store()
			stored, err := store.DeploymentByID(t.Context(), response.ID)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.AppByID(t.Context(), stored.AppID)
			if err != nil {
				t.Fatal(err)
			}
			schedules, err := store.ListScheduledReleases(t.Context(), app.AccountID, app.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			if stored.TrafficPercent != 0 || !stored.TrafficPercentExplicit || len(schedules) != 1 || schedules[0].DeploymentID != stored.ID || !schedules[0].ReleaseAt.Equal(at) {
				t.Fatalf("future release traffic=%d explicit=%v schedules=%+v", stored.TrafficPercent, stored.TrafficPercentExplicit, schedules)
			}
		})
	}
}

func TestSourceTarballSDKPreservesNotifyOnlyValidation(t *testing.T) {
	f := newSourceAdmissionFixture(t)
	_, err := f.client.DeployFromSourceTarball(t.Context(), "x", bytes.NewReader(sourceAdmissionArchive(t, "schema_version: 1\n")), "source.tar.gz", api.SourceTarballDeployRequest{ReleaseNotify: []string{"release-ops"}})
	var remote *api.APIError
	if !errors.As(err, &remote) || remote.Problem.Status != http.StatusBadRequest || remote.Problem.Code != api.CodeValidation {
		t.Fatalf("notification without release_at: %v; want validation 400", err)
	}
	if _, err := (*f.env.Store()).LatestDeployment(t.Context(), *f.env.AppID()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("invalid schedule enqueued a deployment: %v", err)
	}
}

func TestSourceDeploymentMigrationOverrideCutover(t *testing.T) {
	for _, transport := range []string{"multipart", "source-ref", "source-tarball"} {
		for _, override := range []bool{false, true} {
			name := "guarded"
			if override {
				name = "explicit-override"
			}
			t.Run(transport+"/"+name, func(t *testing.T) {
				f := newSourceAdmissionFixture(t)
				store := *f.env.Store()
				appID := *f.env.AppID()
				prior, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100})
				if err != nil {
					t.Fatal(err)
				}
				raw := sourceAdmissionArchive(t, "schema_version: 1\n")
				fullTraffic := 100
				var response api.DeploymentResponse
				switch transport {
				case "multipart":
					response, err = f.client.DeployMultipart(t.Context(), "x", bytes.NewReader(raw), "source.tar.gz", "", "", false, api.DeployAnnotations{MCPTaskMigrationOverride: override, TrafficPercent: &fullTraffic})
				case "source-ref":
					*(*f.env.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(raw)}
					response, err = f.client.DeployFromSourceRef(t.Context(), "x", api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", MCPTaskMigrationOverride: override, TrafficPercent: &fullTraffic})
				case "source-tarball":
					response, err = f.client.DeployFromSourceTarball(t.Context(), "x", bytes.NewReader(raw), "source.tar.gz", api.SourceTarballDeployRequest{MCPTaskMigrationOverride: override, TrafficPercent: &fullTraffic})
				}
				if err != nil {
					t.Fatal(err)
				}
				stored, err := store.DeploymentByID(t.Context(), response.ID)
				if err != nil || stored.MCPTaskMigrationOverride != override {
					t.Fatalf("migration override=%v want=%v err=%v", stored.MCPTaskMigrationOverride, override, err)
				}
				if err := store.PutMCPTaskCompatibilityReport(t.Context(), appID, state.MCPTaskCompatibilityReport{ObservedAt: time.Now(), Handlers: []state.MCPTaskHandlerCompatibility{{ToolName: "charge_order", HandlerVersion: "v1", WaitingTasks: 1}}}); err != nil {
					t.Fatal(err)
				}
				err = store.MarkDeploymentLive(t.Context(), stored.ID)
				if override {
					if err != nil {
						t.Fatal(err)
					}
					updated, err := store.DeploymentByID(t.Context(), stored.ID)
					if err != nil || updated.Status != state.DeployLive || updated.MCPTaskMigrationOverride {
						t.Fatalf("cutover status=%s grant=%v err=%v", updated.Status, updated.MCPTaskMigrationOverride, err)
					}
				} else {
					if !errors.Is(err, state.ErrMCPTaskCompatibilityGate) {
						t.Fatalf("unapproved cutover=%v; want task compatibility gate", err)
					}
					updated, err := store.DeploymentByID(t.Context(), prior.ID)
					if err != nil || updated.Status != state.DeployLive || updated.TrafficPercent != 100 {
						t.Fatalf("serving deployment changed after refusal: %+v err=%v", updated, err)
					}
				}
			})
		}
	}
}

func TestSourceDeploymentEmptyManifest(t *testing.T) {
	for _, transport := range []string{"source-ref", "multipart", "source-tarball", "resumable"} {
		for _, file := range []string{"absent", "gregale.yaml", "gregale.yml", "gregale.toml"} {
			for _, body := range []string{"", " \n\t"} {
				if file == "absent" && body != "" {
					continue
				}
				t.Run(transport+"/"+file+"/"+body, func(t *testing.T) {
					f := newSourceAdmissionFixture(t)
					files := map[string]string{"index.js": "console.log(1)\n"}
					if file != "absent" {
						files[file] = body
					}
					response, err := f.deploy(t, transport, sourceConfigurationArchive(t, files), api.DeployAnnotations{})
					if err != nil || response.ID == "" {
						t.Fatalf("blank or absent manifest: deployment=%s err=%v", response.ID, err)
					}
				})
			}
		}
	}
}
