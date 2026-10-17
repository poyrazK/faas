//go:build metal

package e2e_test

import (
	"testing"
	"testing/fstest"
)

func TestProjectProgressiveServiceGraphPromotionMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "service_promotion", "")
}
func TestProjectProgressiveServiceGraphRollbackMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "service_rollback", "")
}
func TestProjectProgressiveServiceGraphAbortMetal(t *testing.T) {
	qualifyProjectProgressiveFailureMode(t, false, false, true, "service_abort", "")
}

func projectServiceGraphNativeFiles(binary []byte, version string) fstest.MapFS {
	files := projectHTTPNativeFiles(binary, version)
	files["docker-compose.yml"].Data = []byte("services:\n  frontend:\n    build: { context: ., dockerfile: Dockerfile.frontend }\n    ports: [\"8080:8080\"]\n    depends_on: [backend]\n  backend:\n    build: { context: ., dockerfile: Dockerfile.backend }\n    ports: [\"8080:8080\"]\n")
	files["Dockerfile.backend"] = &fstest.MapFile{Data: files["Dockerfile.frontend"].Data}
	return files
}

func assertNativeServiceGraphReport(t *testing.T, report projectWorkerNativeReport) {
	t.Helper()
	if report.BackendGeneration != report.Generation || report.BackendMode != report.Mode || report.BackendMode != report.BackendGeneration+"-config" || report.ReleaseID == "" || report.ReleaseID != report.BackendReleaseID {
		t.Fatalf("mixed managed service graph/configuration: %+v", report)
	}
}
