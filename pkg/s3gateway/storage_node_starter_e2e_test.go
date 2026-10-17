//go:build unix

package s3gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type starterStoredObject struct {
	path, receipt string
	size          int64
}

const starterHoldBytes = 64 << 10

type starterStreamHold struct {
	release, cancelled chan struct{}
	once               sync.Once
}

func (h *starterStreamHold) finish() { h.once.Do(func() { close(h.release) }) }

type starterProvider struct {
	directory string
	mu        sync.Mutex
	objects   map[string]starterStoredObject
	holds     map[string]*starterStreamHold
	listHold  *starterListingHold
	failList  bool
	listConns []string
	reads     map[string]int
}

type starterListingHold struct {
	entered, release, cancelled chan struct{}
	once                        sync.Once
	cancelOnce                  sync.Once
}

func (p *starterProvider) listing(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	hold, fail := p.listHold, p.failList
	p.mu.Unlock()
	if hold != nil {
		hold.once.Do(func() { close(hold.entered) })
		select {
		case <-hold.release:
		case <-r.Context().Done():
			hold.cancelOnce.Do(func() { close(hold.cancelled) })
			return
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	if fail {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
		return
	}
	_, _ = io.WriteString(w, `<ListBucketResult><Name>physical</Name><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated></ListBucketResult>`)
}

func (p *starterProvider) blockListing(t *testing.T) *starterListingHold {
	t.Helper()
	hold := &starterListingHold{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	p.mu.Lock()
	p.listHold = hold
	p.mu.Unlock()
	t.Cleanup(func() { p.unblockListing(hold) })
	return hold
}

func (p *starterProvider) unblockListing(hold *starterListingHold) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listHold == hold {
		p.listHold = nil
		close(hold.release)
	}
}

func (p *starterProvider) listConnection() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listConns[len(p.listConns)-1]
}

func (p *starterProvider) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		p.listing(w, r)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/physical/")
	if key == r.URL.Path || strings.Contains(key, "/") || key == "" {
		t.Error("unexpected provider path")
		w.WriteHeader(500)
		return
	}
	if r.Method == http.MethodPut {
		p.put(t, w, r, key)
		return
	}
	p.read(t, w, r, key)
}

func (p *starterProvider) put(t *testing.T, w http.ResponseWriter, r *http.Request, key string) {
	t.Helper()
	path := filepath.Join(p.directory, key)
	file, err := os.Create(path)
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	n, copyErr := io.Copy(file, r.Body)
	closeErr := file.Close()
	// The Go S3 transport can frame a known-empty reader with a terminating
	// HTTP chunk instead of Content-Length: 0. Both represent the same empty
	// object; nonempty fixture writes still require an exact wire length.
	emptyChunked := n == 0 && r.ContentLength == -1
	if copyErr != nil || closeErr != nil || n != r.ContentLength && !emptyChunked {
		t.Error("provider fixture write failed", copyErr, closeErr)
		w.WriteHeader(500)
		return
	}
	p.mu.Lock()
	p.objects[key] = starterStoredObject{path: path, size: n, receipt: r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)}
	p.mu.Unlock()
	w.Header().Set("ETag", `"starter-fixture"`)
}

func (p *starterProvider) read(t *testing.T, w http.ResponseWriter, r *http.Request, key string) {
	t.Helper()
	p.mu.Lock()
	object, exists := p.objects[key]
	hold := p.holds[key]
	if r.Method == http.MethodGet {
		p.reads[key]++
	}
	p.mu.Unlock()
	if !exists {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(object.size, 10))
	w.Header().Set("ETag", `"starter-fixture"`)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, object.receipt)
	if r.Method == http.MethodHead {
		return
	}
	if r.Method != http.MethodGet {
		t.Error("unexpected provider method", r.Method)
		w.WriteHeader(500)
		return
	}
	file, err := os.Open(object.path) //nolint:forbidigo // Owned fixture file only.
	if err != nil {
		t.Error(err)
		return
	}
	defer file.Close()
	if hold != nil {
		_, _ = io.CopyN(w, file, starterHoldBytes)
		w.(http.Flusher).Flush()
		select {
		case <-hold.release:
		case <-r.Context().Done():
			close(hold.cancelled)
			return
		}
	}
	_, _ = io.Copy(w, file)
}

type starterAck struct{ Revision, Status, Generation string }

type starterGatewayWriter struct {
	http.ResponseWriter
	status    int
	errorBody []byte
}

func (w *starterGatewayWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *starterGatewayWriter) Write(body []byte) (int, error) {
	if w.status >= 400 && len(w.errorBody) < 4096 {
		w.errorBody = append(w.errorBody, body[:min(len(body), 4096-len(w.errorBody))]...)
	}
	return w.ResponseWriter.Write(body)
}
func (w *starterGatewayWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *starterGatewayWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }

type starterProcess struct {
	command  *exec.Cmd
	exit     <-chan error
	acks     <-chan starterAck
	base     string
	snapshot string
	endpoint string
}

func startStorageStarter(t *testing.T, directory, endpoint string) *starterProcess {
	t.Helper()
	acks := make(chan starterAck, 16)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ack starterAck
		if r.Method != http.MethodPost || json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&ack) != nil {
			w.WriteHeader(400)
			return
		}
		acks <- ack
		w.WriteHeader(202)
	}))
	t.Cleanup(metadata.Close)
	private := t.TempDir()
	p := &starterProcess{snapshot: filepath.Join(private, "snapshot.json"), endpoint: endpoint, acks: acks}
	p.publish(t, "a", testAccess)
	command := exec.CommandContext(t.Context(), "node", "server.mjs")
	command.Dir = directory
	command.Env = append(os.Environ(), "PORT=0", "AWS_EC2_METADATA_DISABLED=true", "FAAS_SECRETS_SNAPSHOT_FILE="+p.snapshot,
		"FAAS_SECRETS_RELOAD_READY_FILE="+filepath.Join(private, "ready"), "FAAS_SECRETS_RELOAD_ACK_ENDPOINT="+metadata.URL,
		"FAAS_SECRETS_RELOAD_GENERATION="+strings.Repeat("e", 32))
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Output consists only of the starter's stable, sanitized event codes.
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	exit := make(chan error, 1)
	go func() { exit <- command.Wait(); close(exit) }()
	p.command, p.exit = command, exit
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-exit:
		case <-time.After(3 * time.Second):
			t.Error("owned starter did not exit")
		}
	})
	listening := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			var event struct {
				Event string
				Port  int
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Event == "listening" {
				listening <- event.Port
			}
		}
	}()
	select {
	case port := <-listening:
		p.base = "http://127.0.0.1:" + strconv.Itoa(port)
	case <-time.After(10 * time.Second):
		t.Fatal("starter did not become ready")
	}
	p.ack(t, "a", "applied")
	return p
}

func (p *starterProcess) publish(t *testing.T, revision, access string) {
	t.Helper()
	secrets := map[string]string{"GREGALE_S3_ASSETS_ENDPOINT": p.endpoint, "GREGALE_S3_ASSETS_REGION": "us-east-1",
		"GREGALE_S3_ASSETS_BUCKET": "assets", "GREGALE_S3_ASSETS_ACCESS_KEY_ID": access, "GREGALE_S3_ASSETS_SECRET_ACCESS_KEY": testSecret,
		"GREGALE_S3_ASSETS_ADDRESSING_STYLE": "path", "STORAGE_APP_TOKEN": strings.Repeat("a", 48)}
	body, err := json.Marshal(map[string]any{"revision": strings.Repeat(revision, 64), "secrets": secrets})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p.snapshot+".next", body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(p.snapshot+".next", p.snapshot); err != nil {
		t.Fatal(err)
	}
}

func (p *starterProcess) reload(t *testing.T, revision, access, status string) {
	t.Helper()
	p.publish(t, revision, access)
	if err := p.command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	p.ack(t, revision, status)
}

func (p *starterProcess) ack(t *testing.T, revision, status string) {
	t.Helper()
	select {
	case ack := <-p.acks:
		if ack.Revision != strings.Repeat(revision, 64) || ack.Status != status || ack.Generation != strings.Repeat("e", 32) {
			t.Fatal("unexpected revision-bound adoption receipt")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("starter adoption receipt timed out")
	}
}

func (p *starterProcess) request(t *testing.T, method, path string, body []byte, status int) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, p.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 48))
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != status {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		t.Fatalf("%s %s: status %d, want %d, body %s", method, path, response.StatusCode, status, data)
	}
	return response
}

func starterBytes(t *testing.T, response *http.Response, expected []byte) {
	t.Helper()
	got, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("response bytes mismatch: %d bytes, %v", len(got), err)
	}
}

func starterHeld(t *testing.T, p *starterProcess, key string) *http.Response {
	t.Helper()
	response := p.request(t, http.MethodGet, "/objects/"+key, nil, 200)
	first := make([]byte, starterHoldBytes)
	if _, err := io.ReadFull(response.Body, first); err != nil || !bytes.Equal(first, bytes.Repeat([]byte("x"), starterHoldBytes)) {
		t.Fatal("held response did not forward its first bytes", err)
	}
	return response
}

func starterCredential(t *testing.T, st *state.MemStore, bucket state.ObjectBucket, access string) state.ObjectS3Credential {
	t.Helper()
	credential, err := st.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: bucket.AccountID,
		BucketID: bucket.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "test", Label: "starter", Permission: state.ObjectBucketPermissionReadWrite,
		Status: state.ObjectS3CredentialStatusActive}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

// adr: 1034
func TestManagedStorageNodeStarterJourney(t *testing.T) {
	directory := os.Getenv("GREGALE_STORAGE_NODE_STARTER_FIXTURE")
	if directory == "" {
		t.Skip("run scripts/test-storage-node-starter.sh to install and qualify the embedded Node starter")
	}
	if !filepath.IsAbs(directory) {
		t.Fatal("starter fixture path must be absolute")
	}
	provider := &starterProvider{directory: t.TempDir(), objects: map[string]starterStoredObject{}, holds: map[string]*starterStreamHold{}, reads: map[string]int{}}
	for _, key := range []string{"rotate.bin", "revoke.bin", "restore.bin", "cancel.bin", "shutdown.bin"} {
		hold := &starterStreamHold{release: make(chan struct{}), cancelled: make(chan struct{})}
		provider.holds[key] = hold
		t.Cleanup(hold.finish)
	}
	st := state.NewMemStore()
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { provider.serve(t, w, r) }), transferIntegrationConfig(30), 0)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			provider.mu.Lock()
			provider.listConns = append(provider.listConns, r.RemoteAddr)
			provider.mu.Unlock()
		}
		observed := &starterGatewayWriter{ResponseWriter: w, status: 200}
		f.handler.ServeHTTP(observed, r)
		if observed.status >= 400 {
			var failure struct{ Code string }
			_ = xml.Unmarshal(observed.errorBody, &failure)
			t.Logf("gateway failure: %s path=%s status=%d code=%s", r.Method, r.URL.EscapedPath(), observed.status, failure.Code)
		}
	}))
	t.Cleanup(gateway.Close)
	address, _ := url.Parse(gateway.URL)
	f.handler.host = address.Host
	p := startStorageStarter(t, directory, gateway.URL)
	readiness := p.request(t, "GET", "/readyz", nil, 200)
	defer readiness.Body.Close()
	starterBytes(t, readiness, []byte(`{"ok":true,"code":"ready"}`))
	data := bytes.Repeat([]byte{0, 255, 13, 10, 42}, 16384)
	upload := p.request(t, "PUT", "/objects/binary.bin", data, 201)
	_ = upload.Body.Close()
	download := p.request(t, "GET", "/objects/binary.bin", nil, 200)
	if download.Header.Get("Content-Disposition") != "attachment" || download.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatal("download did not enforce opaque attachment headers")
	}
	starterBytes(t, download, data)
	_ = p.request(t, "PUT", "/objects/empty.bin", nil, 201).Body.Close()
	empty := p.request(t, "GET", "/objects/empty.bin", nil, 200)
	defer empty.Body.Close()
	starterBytes(t, empty, nil)
	for key := range provider.holds {
		response := p.request(t, "PUT", "/objects/"+key, append(bytes.Repeat([]byte("x"), starterHoldBytes), []byte("last")...), 201)
		_ = response.Body.Close()
	}
	starterRotationJourney(t, p, st, f.bucket, provider)
}

func starterRotationJourney(t *testing.T, p *starterProcess, st *state.MemStore, bucket state.ObjectBucket, provider *starterProvider) {
	t.Helper()
	held := starterHeld(t, p, "rotate.bin")
	defer held.Body.Close()
	rotated := starterCredential(t, st, bucket, "GRGAAAAAAAAAAAAAAAAB")
	p.reload(t, "b", rotated.AccessKeyID, "applied")
	previous, _, err := st.ResolveObjectS3Credential(t.Context(), testAccess)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RevokeObjectS3Credential(t.Context(), bucket.AccountID, bucket.ID, previous.ID); err != nil {
		t.Fatal(err)
	}
	response := p.request(t, "GET", "/objects/binary.bin", nil, 200)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	provider.holds["rotate.bin"].finish()
	starterBytes(t, held, []byte("last"))
	held = starterHeld(t, p, "revoke.bin")
	defer held.Body.Close()
	if err = st.RevokeObjectS3Credential(t.Context(), bucket.AccountID, bucket.ID, rotated.ID); err != nil {
		t.Fatal(err)
	}
	p.reload(t, "c", rotated.AccessKeyID, "failed")
	_ = p.request(t, "GET", "/readyz", nil, 503).Body.Close()
	_ = p.request(t, "GET", "/objects/binary.bin", nil, 503).Body.Close()
	provider.holds["revoke.bin"].finish()
	starterBytes(t, held, []byte("last"))
	recovered := starterCredential(t, st, bucket, "GRGAAAAAAAAAAAAAAAAC")
	p.reload(t, "d", recovered.AccessKeyID, "applied")
	_ = p.request(t, "GET", "/readyz", nil, 200).Body.Close()
	starterRestoreJourney(t, p, provider, recovered.AccessKeyID)
	starterStopJourney(t, p, provider)
}

type starterHookResult struct {
	status int
	err    error
}

func (p *starterProcess) hook(t *testing.T) <-chan starterHookResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, p.base+"/internal/after-restore", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Faas-After-Restore", "1")
	result := make(chan starterHookResult, 1)
	go func() {
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			result <- starterHookResult{err: err}
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		result <- starterHookResult{status: response.StatusCode}
	}()
	return result
}

func starterHookStatus(t *testing.T, result <-chan starterHookResult, status int) {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil || got.status != status {
			t.Fatalf("restore hook: status %d, want %d, err=%v", got.status, status, got.err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("restore hook did not return within its application budget")
	}
}

func starterRestoreJourney(t *testing.T, p *starterProcess, provider *starterProvider, access string) {
	t.Helper()
	held := starterHeld(t, p, "restore.bin")
	defer held.Body.Close()
	oldConnection := provider.listConnection()
	starterHookStatus(t, p.hook(t), 204)
	p.ack(t, "d", "applied") // Same revision, fresh real AWS SDK transport.
	if provider.listConnection() == oldConnection {
		t.Fatal("same-revision restore reused the captured gateway connection")
	}
	select {
	case <-provider.holds["restore.bin"].cancelled:
	case <-time.After(time.Second):
		t.Fatal("restore did not cancel the captured provider stream")
	}
	if data, err := io.ReadAll(held.Body); err == nil || len(data) != 0 {
		t.Fatal("captured download unexpectedly completed after restore")
	}
	provider.mu.Lock()
	reads := provider.reads["restore.bin"]
	provider.failList = true
	provider.mu.Unlock()
	if reads != 1 {
		t.Fatal("captured download was replayed")
	}
	starterHookStatus(t, p.hook(t), 503)
	p.ack(t, "d", "failed")
	_ = p.request(t, "GET", "/readyz", nil, 503).Body.Close()
	provider.mu.Lock()
	provider.failList = false
	provider.mu.Unlock()
	p.reload(t, "d", access, "applied")
	_ = p.request(t, "GET", "/readyz", nil, 503).Body.Close()
	starterHookStatus(t, p.hook(t), 204)
	p.ack(t, "d", "applied")
	starterRestoreTimeout(t, p, provider, access)
	starterQueuedRestoreTimeout(t, p, provider, access)
}

func starterRestoreTimeout(t *testing.T, p *starterProcess, provider *starterProvider, access string) {
	t.Helper()
	hold := provider.blockListing(t)
	p.publish(t, "e", access)
	started := time.Now()
	result := p.hook(t)
	select {
	case <-hold.entered:
	case <-time.After(time.Second):
		t.Fatal("restore did not validate a new signed client")
	}
	_ = p.request(t, "GET", "/readyz", nil, 503).Body.Close()
	_ = p.request(t, "GET", "/objects/binary.bin", nil, 503).Body.Close()
	starterHookStatus(t, result, 503)
	if time.Since(started) > 2500*time.Millisecond {
		t.Fatal("restore exceeded its application hook budget")
	}
	select {
	case <-hold.cancelled:
	case <-time.After(time.Second):
		t.Fatal("timed-out validation left the provider request running")
	}
	provider.unblockListing(hold)
	select {
	case <-p.acks:
		t.Fatal("cancelled validation emitted an adoption ACK")
	default:
	}
	p.publish(t, "f", access)
	starterHookStatus(t, p.hook(t), 204)
	p.ack(t, "f", "applied")
	_ = p.request(t, "GET", "/readyz", nil, 200).Body.Close()
}

func starterQueuedRestoreTimeout(t *testing.T, p *starterProcess, provider *starterProvider, access string) {
	t.Helper()
	hold := provider.blockListing(t)
	p.publish(t, "1", access)
	if err := p.command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hold.entered:
	case <-time.After(time.Second):
		t.Fatal("SIGHUP did not enter validation")
	}
	starterHookStatus(t, p.hook(t), 503)
	_ = p.request(t, "GET", "/readyz", nil, 503).Body.Close()
	provider.unblockListing(hold)
	p.ack(t, "1", "failed") // The captured candidate was fenced synchronously.
	p.publish(t, "2", access)
	starterHookStatus(t, p.hook(t), 204)
	p.ack(t, "2", "applied")
	_ = p.request(t, "GET", "/readyz", nil, 200).Body.Close()
}

func starterStopJourney(t *testing.T, p *starterProcess, provider *starterProvider) {
	t.Helper()
	held := starterHeld(t, p, "cancel.bin")
	_ = held.Body.Close()
	select {
	case <-provider.holds["cancel.bin"].cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("client disconnect did not cancel the provider body")
	}
	held = starterHeld(t, p, "shutdown.bin")
	defer held.Body.Close()
	if err := p.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	starterAwaitDrain(t, p)
	select {
	case <-p.exit:
		t.Fatal("starter exited before its admitted stream drained")
	default:
	}
	provider.holds["shutdown.bin"].finish()
	starterBytes(t, held, []byte("last"))
	select {
	case err := <-p.exit:
		if err != nil {
			t.Fatal("graceful starter shutdown failed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("starter did not drain and exit")
	}
}

func starterAwaitDrain(t *testing.T, p *starterProcess) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	client := &http.Client{Timeout: 100 * time.Millisecond}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.base+"/readyz", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err != nil {
			return
		} // Listener closed: new admission stopped.
		_ = response.Body.Close()
		if response.StatusCode == 503 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("starter did not stop new admission")
}
