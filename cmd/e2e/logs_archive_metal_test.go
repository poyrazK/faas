//go:build metal

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// logBucket is an in-process S3-compatible bucket (path-style PUT, GET and a
// ListObjects listing) standing in for the runtime-log archive. Readers list
// a day's part objects before reading its whole-day object
// (ADR-20261028110000), so without the listing every archived day read back
// as missing.
type logBucket struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (b *logBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/logs-e2e/")
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Has("prefix"):
		b.listLocked(w, r)
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		b.objects[key] = body
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet:
		object, ok := b.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`))
			return
		}
		_, _ = w.Write(object)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// listLocked answers a ListObjects (V1) request: the keys under prefix after
// marker, in order, at most max-keys of them.
func (b *logBucket) listLocked(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	prefix, marker := query.Get("prefix"), query.Get("marker")
	limit, err := strconv.Atoi(query.Get("max-keys"))
	if err != nil || limit <= 0 {
		limit = 1000
	}
	var keys []string
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) && key > marker {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	type content struct {
		Key string `xml:"Key"`
	}
	result := struct {
		XMLName     xml.Name  `xml:"ListBucketResult"`
		IsTruncated bool      `xml:"IsTruncated"`
		Contents    []content `xml:"Contents"`
	}{IsTruncated: len(keys) > limit}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	for _, key := range keys {
		result.Contents = append(result.Contents, content{Key: key})
	}
	body, err := xml.Marshal(result)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(body)
}

func (b *logBucket) has(prefix string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// logFrame is one `event: log` payload of GET /v1/apps/{slug}/logs.
type logFrame struct {
	Instance  string `json:"instance"`
	Line      string `json:"line"`
	WrittenAt string `json:"written_at"`
}

// readLogStream reads one SSE logs response to its end and returns the log
// frames and the terminal `event: end` payload.
func readLogStream(t *testing.T, h *e2etest.Harness, key, path string) ([]logFrame, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.GatewayURL+path, nil)
	if err != nil {
		t.Fatalf("new logs request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, string(body), resp.StatusCode
	}
	var frames []logFrame
	event, end := "", ""
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && event == "log":
			var frame logFrame
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
				t.Fatalf("decode log frame %q: %v", line, err)
			}
			frames = append(frames, frame)
		case strings.HasPrefix(line, "data: ") && (event == "end" || event == "degraded" || event == "error"):
			end = event + " " + strings.TrimPrefix(line, "data: ")
			return frames, end, resp.StatusCode
		}
	}
	return frames, end, resp.StatusCode
}

func frameWith(frames []logFrame, marker string) (logFrame, bool) {
	for _, frame := range frames {
		if strings.Contains(frame.Line, marker) {
			return frame, true
		}
	}
	return logFrame{}, false
}

// TestLogsLiveAndArchivedMetal is the Logs capability's customer-path
// evidence: a line the app writes to stdout while serving a request reaches
// GET /v1/apps/{slug}/logs from the running instance, and after the app
// parks the same line reaches the archive and reads back through
// GET /v1/apps/{slug}/logs?archive=1 (vmmd retire → spool → shipper →
// bucket → gatewayd-internal).
func TestLogsLiveAndArchivedMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping logs end-to-end")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping logs end-to-end")
	}

	bucket := &logBucket{objects: map[string][]byte{}}
	bucketSrv := httptest.NewServer(bucket)
	t.Cleanup(bucketSrv.Close)
	root := t.TempDir()
	creds, err := json.Marshal(map[string]string{
		"endpoint": bucketSrv.URL, "region": "us-east-1", "bucket": "logs-e2e",
		"key_id": "e2e-key", "secret": "e2e-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	credsPath := filepath.Join(root, "archive-creds.json")
	if err := os.WriteFile(credsPath, creds, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_LOG_ARCHIVE_CREDS_PATH", credsPath)
	t.Setenv("FAAS_LOG_ARCHIVE_INTERVAL", "2s")
	t.Setenv("FAAS_LOG_ARCHIVE_SPOOL_ROOT", filepath.Join(root, "spool-apid"))
	t.Setenv("FAAS_VMMD_LOG_ARCHIVE_SPOOL_ROOT", filepath.Join(root, "spool-vmmd"))

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := "logs-e2e"
	runBuildSubtest(t, h, pool, key, slug, "logs-e2e", logsMarkerFixture(t), false)
	appID := mustGetAppID(t, h, key, slug)

	marker := "logs-e2e-" + uuid.NewString()
	if body, status := doGetWithHost(t, h.HTTPClient(), h.GatewayURL+"/"+marker, slug+".apps.test.example", 60*time.Second); status != http.StatusOK {
		t.Fatalf("request %s: status=%d body=%s", marker, status, body)
	}

	// Live: the running instance's ring holds the line.
	var live logFrame
	deadline := time.Now().Add(20 * time.Second)
	for {
		frames, end, status := readLogStream(t, h, key, "/v1/apps/"+slug+"/logs")
		if frame, ok := frameWith(frames, marker); ok {
			live = frame
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live logs never showed %q: status=%d, %d frames, end=%s", marker, status, len(frames), end)
		}
		time.Sleep(time.Second)
	}
	if live.Instance == "" || live.WrittenAt == "" {
		t.Fatalf("live frame lacks instance or time: %+v", live)
	}

	// Park: vmmd retires the ring into the spool and the shipper uploads it.
	// apid answers 204 once the instances have drained; 202 means the park
	// was only queued.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status {
	case http.StatusNoContent:
	case http.StatusAccepted:
		if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 50*time.Second); err != nil {
			t.Fatalf("park %s: %v", slug, err)
		}
	default:
		t.Fatalf("park %s: status=%d body=%s", slug, status, body)
	}
	prefix := "faas-logs/" + live.Instance + "/"
	for !bucket.has(prefix) {
		if ctx.Err() != nil {
			t.Fatalf("no archive object under %s after the app parked", prefix)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Archived: the same line reads back through the customer route.
	writtenAt, err := time.Parse(time.RFC3339Nano, live.WrittenAt)
	if err != nil {
		t.Fatalf("parse written_at %q: %v", live.WrittenAt, err)
	}
	day := writtenAt.UTC().Format("2006-01-02")
	frames, end, status := readLogStream(t, h, key, "/v1/apps/"+slug+"/logs?archive=1&instance="+live.Instance+"&date="+day)
	if status != http.StatusOK {
		t.Fatalf("archive read: status=%d body=%s", status, end)
	}
	if _, ok := frameWith(frames, marker); !ok {
		t.Fatalf("archived logs for %s on %s lack %q: %d frames, end=%s", live.Instance, day, marker, len(frames), end)
	}
	if !strings.Contains(end, "archive_complete") {
		t.Fatalf("archive read ended %q, want archive_complete", end)
	}
}

// logsMarkerFixture is a Node app that writes each request's path to stdout.
func logsMarkerFixture(t *testing.T) []byte {
	t.Helper()
	const pkgJSON = `{
  "name": "faas-fixture-logs",
  "version": "1.0.0",
  "private": true,
  "engines": {"node": "22"},
  "scripts": {"start": "node index.js"},
  "dependencies": {}
}
`
	const indexJS = `const http = require('http');
http.createServer((req, res) => {
  if (req.url !== '/healthz') console.log('request ' + req.url.slice(1));
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end('ok\n');
}).listen(3000, () => console.log('logs fixture listening on :3000'));
`
	return buildTarGz(t, map[string]string{
		"package.json":     pkgJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
