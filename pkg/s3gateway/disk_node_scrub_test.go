package s3gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
)

func TestDiskNodeScrubPrivateAPIAndMetrics(t *testing.T) {
	store, h := diskFixture(t)
	if err := store.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	o, err := store.Put(t.Context(), "test-bucket", "private-key", strings.NewReader("original"), 8, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, file, err := store.Get("test-bucket", o.Key)
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	if err = os.WriteFile(path, []byte("damaged!"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedInventoryRequest(t, method, path))
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private observations were cacheable")
		}
		return w
	}
	start := request(http.MethodPost, "/?gregale-inventory=scrub", 200)
	var status objectnode.ScrubStatus
	if json.Unmarshal(start.Body.Bytes(), &status) != nil || status.State != "running" || status.Targets != 1 {
		t.Fatal(start.Body.String())
	}
	request(http.MethodPost, "/?gregale-inventory=scrub", 409)
	for range 4 {
		status, err = store.ScrubStep(t.Context(), 4)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "complete" {
			break
		}
	}
	if status.Failures != 1 || status.State != "complete" {
		t.Fatal(status)
	}
	get := request(http.MethodGet, "/?gregale-inventory=scrub", 200)
	if json.Unmarshal(get.Body.Bytes(), &status) != nil || status.Failures != 1 {
		t.Fatal(get.Body.String())
	}
	findings := request(http.MethodGet, "/?gregale-inventory=scrub-findings&max-keys=1", 200)
	var page objectnode.ScrubFindingsPage
	if json.Unmarshal(findings.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Target.BlobID != o.ID || !page.Items[0].Current || page.Items[0].Reason != "checksum_mismatch" {
		t.Fatal(findings.Body.String())
	}
	if _, err = store.Put(t.Context(), "test-bucket", o.Key, strings.NewReader("replacement"), 11, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	findings = request(http.MethodGet, "/?gregale-inventory=scrub-findings", 200)
	if json.Unmarshal(findings.Body.Bytes(), &page) != nil || page.Items[0].Current || !page.Items[0].CurrentKnown {
		t.Fatal("stale evidence was attributed to a replacement", findings.Body.String())
	}
	h, err = NewDiskHandler(h.config)
	if err != nil {
		t.Fatal(err)
	}
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{`faas_object_node_scrub_targets{outcome="failed"} 1`, `faas_object_node_scrub_bytes{kind="read"} 8`, `faas_object_node_scrub_running 0`, `faas_object_node_scrub_timestamp_seconds{event="verified_manifest"} 0`} {
		if !strings.Contains(metrics.Body.String(), expected) {
			t.Fatal("missing scrub progress metric", expected, metrics.Body.String())
		}
	}
	if strings.Contains(metrics.Body.String(), "private-key") || strings.Contains(metrics.Body.String(), o.ID) {
		t.Fatal("metrics leaked a blob identity")
	}
	for _, target := range []string{"/test-bucket?gregale-inventory=scrub", "/?gregale-inventory=scrub&cursor=foreign", "/?gregale-inventory=scrub-findings&max-keys=0", "/?gregale-inventory=scrub&gregale-inventory=usage"} {
		request(http.MethodGet, target, 400)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?gregale-inventory=scrub-findings", nil))
	if w.Code != 403 {
		t.Fatal("unsigned scrub findings escaped", w.Code)
	}
}
