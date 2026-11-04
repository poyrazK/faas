package apidserver_test

// adr: 20261029203000 — OTLP metric exports may be gzip-compressed, as the
// OpenTelemetry Collector sends them by default; the 1 MiB bound holds once
// decompressed, and an encoding other than gzip is refused.

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPostOTLPMetrics_AcceptsGzip(t *testing.T) {
	e := apidserver.CustomHistoryEnv(t)
	apidserver.CreateApp(t, e, "shop")
	export := []byte(fmt.Sprintf(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"queue.depth","gauge":{"dataPoints":[{"asDouble":4,"timeUnixNano":"%d"}]}}]}]}]}`, time.Now().UnixNano()))

	if rec := apidserver.PostOTLPEncoded(t, e, "application/json", "gzip", gzipped(t, export)); rec.Code != http.StatusOK {
		t.Fatalf("gzip export = %d %s, want 200", rec.Code, rec.Body)
	}
	rec := e.Do(t, http.MethodGet, "/v1/apps/shop/custom-metrics", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queue_depth"`) {
		t.Fatalf("custom metrics after a gzip export = %d %s, want queue_depth", rec.Code, rec.Body)
	}

	for _, tc := range []struct {
		name, encoding string
		body           []byte
		want           int
	}{
		{"an unknown encoding", "br", export, http.StatusUnsupportedMediaType},
		{"a body that is not gzip", "gzip", export, http.StatusBadRequest},
		{"a bomb past the limit once decompressed", "gzip", gzipped(t, bytes.Repeat([]byte(" "), api.OTLPMetricsMaxBodyBytes+1)), http.StatusRequestEntityTooLarge},
	} {
		if rec := apidserver.PostOTLPEncoded(t, e, "application/json", tc.encoding, tc.body); rec.Code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}
