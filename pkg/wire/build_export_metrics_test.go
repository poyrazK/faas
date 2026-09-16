package wire

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildExportMetricsContract(t *testing.T) {
	ops := NewOpsMetrics("builderd")
	ops.ObserveBuildExportCleanup("released", 2)
	ops.ObserveBuildExportCleanup("unbounded-label", 99)
	ops.ObserveBuildExportCleanupErrors(3)
	ops.SetBuildExportBytes(4096)
	rr := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(rr.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`builderd_build_export_cleanup_total{reason="released"} 2`,
		`builderd_build_export_cleanup_total{reason="expired"} 0`,
		`builderd_build_export_cleanup_errors_total 3`,
		`builderd_build_export_bytes 4096`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if strings.Contains(text, "unbounded-label") {
		t.Fatal("cleanup metric accepted an unbounded reason label")
	}
}
