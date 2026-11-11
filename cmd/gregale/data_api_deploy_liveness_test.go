package main_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A Data API deploy keeps startup readiness on /healthz (SQL ready) and moves
// the liveness probe to /livez: three SQL-unready answers from a busy pool or
// a database blip must not destroy a healthy runtime.
func TestDataAPIDeploySendsProcessLivenessProbe(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var liveness api.DeploymentLivenessProbe
	var startup api.DeploymentHealthcheck
	posted := false
	stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps/notes-data" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "notes-data", Type: "app"})
		case r.URL.Path == "/v1/apps/notes-data/deployments" && r.Method == http.MethodPost:
			posted = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal([]byte(r.FormValue("liveness_probe")), &liveness); err != nil {
				t.Errorf("liveness_probe = %q: %v", r.FormValue("liveness_probe"), err)
			}
			if err := json.Unmarshal([]byte(r.FormValue("healthcheck")), &startup); err != nil {
				t.Errorf("healthcheck = %q: %v", r.FormValue("healthcheck"), err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	})
	t.Setenv("FAAS_API", (*stub.Srv()).URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	main.WithResetJSONOutput(t, true)
	// Only the submitted form matters; a short deploy timeout ends the wait.
	main.DeployDataAPIArchive(context.Background(), "notes-data", api.DefaultEnvScope, "2s", writeDataAPITestArchive)
	if !posted {
		t.Fatal("the deployment was not submitted")
	}
	if liveness.Path != api.DataAPILivenessPath || liveness.GRPC != nil {
		t.Fatalf("liveness = %+v, want path %s", liveness, api.DataAPILivenessPath)
	}
	if startup.Path != "/healthz" {
		t.Fatalf("startup readiness = %+v, want /healthz", startup)
	}
}

func writeDataAPITestArchive(dest string) error {
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	zipped := gzip.NewWriter(file)
	archive := tar.NewWriter(zipped)
	body := "FROM scratch\n"
	if err = archive.WriteHeader(&tar.Header{Name: "data-api/Dockerfile", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err = strings.NewReader(body).WriteTo(archive); err != nil {
		return err
	}
	if err = archive.Close(); err != nil {
		return err
	}
	return zipped.Close()
}
