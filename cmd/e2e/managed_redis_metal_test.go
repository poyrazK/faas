//go:build metal

// adr: 20261116192126 — managed Redis caches on Valkey, end to end on a real
// guest: create, use from another app over its private address, park and
// wake with the data kept, restart, and resize through the replication
// handoff, with the data kept at every step.
//
// The cache runs the pinned docker.io Valkey image, so the host must be able
// to pull from Docker Hub.
package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type redisMetalReply struct {
	Replies []string `json:"replies"`
	Error   string   `json:"error,omitempty"`
	status  int
}

func TestManagedRedisMetal(t *testing.T) {
	f := newPrivateTCPImageFixture(t)
	if f == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	store := state.NewPgStore(f.pool)
	name := "redis-" + randHexSuffix()

	// Create the cache and wait for its Valkey deployment.
	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/redis", api.CreateRedisCacheRequest{Name: name, MemoryMB: 64})
	if status != http.StatusCreated {
		t.Fatalf("create cache: status=%d body=%s", status, raw)
	}
	cacheApp, err := store.AppBySlug(ctx, name)
	if err != nil {
		t.Fatalf("load cache app: %v", err)
	}
	t.Cleanup(func() { f.parkAtCleanup(t, cacheApp) })
	waitRedisDeploymentLive(t, ctx, f, store, cacheApp.ID, "")
	index, err := store.AppServiceAddressIndex(ctx, cacheApp.ID)
	if err != nil {
		t.Fatalf("service address index: %v", err)
	}
	address, ok := api.ServiceAddressForIndex(index)
	if !ok {
		t.Fatalf("cache %s has no service address", name)
	}
	redis := func(step string, cmds ...string) []string {
		t.Helper()
		got := redisMetalDo(t, f, address.String(), cmds...)
		if got.status != http.StatusOK || len(got.Replies) != len(cmds) {
			f.h.DumpLogs(t)
			t.Fatalf("%s: %v -> status=%d replies=%q err=%s", step, cmds, got.status, got.Replies, got.Error)
		}
		return got.Replies
	}

	// 1. It works, and platform commands are refused.
	got := redis("first use", "SET greeting hello", "GET greeting", "CONFIG GET maxmemory")
	if got[0] != "+OK\r\n" || got[1] != "$5\r\nhello\r\n" {
		t.Fatalf("first use: %q", got)
	}
	if !strings.Contains(got[2], "not allowed") {
		t.Fatalf("CONFIG reached the cache: %q", got[2])
	}
	t.Log("step 1 PASS: SET/GET through the private address; CONFIG refused")

	// 2. Park and wake: the memory snapshot keeps the data.
	f.park(t, cacheApp)
	if got := redis("after park", "GET greeting"); got[0] != "$5\r\nhello\r\n" {
		t.Fatalf("after park and wake: %q, want the key kept", got)
	}
	t.Log("step 2 PASS: data kept across park and wake")

	// 3. Restart: snapshot restart keeps the data.
	if raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/redis/"+name+"/restart", nil); status != http.StatusAccepted {
		t.Fatalf("restart: status=%d body=%s", status, raw)
	}
	time.Sleep(10 * time.Second)
	if got := redis("after restart", "GET greeting"); got[0] != "$5\r\nhello\r\n" {
		t.Fatalf("after restart: %q, want the key kept", got)
	}
	t.Log("step 3 PASS: data kept across restart")

	// 4. Resize through the replication handoff: data kept, new size in force.
	before, err := store.LatestDeployment(ctx, cacheApp.ID)
	if err != nil {
		t.Fatalf("deployment before resize: %v", err)
	}
	redis("before resize", "SET counter 41", "INCR counter")
	raw, status = doReq(t, f.h, f.key, http.MethodPatch, "/v1/redis/"+name, api.UpdateRedisCacheRequest{MemoryMB: 128})
	if status != http.StatusAccepted {
		t.Fatalf("resize: status=%d body=%s", status, raw)
	}
	var change api.RedisCacheChange
	if err := json.Unmarshal(raw, &change); err != nil || change.DeploymentID == "" {
		t.Fatalf("resize response: %s", raw)
	}
	waitRedisDeploymentLive(t, ctx, f, store, cacheApp.ID, change.DeploymentID)
	got = redis("after resize", "GET greeting", "GET counter", "INFO memory")
	if got[0] != "$5\r\nhello\r\n" || got[1] != "$2\r\n42\r\n" {
		f.h.DumpLogs(t)
		t.Fatalf("after resize: %q, want both keys kept", got[:2])
	}
	if !strings.Contains(got[2], "maxmemory:"+strconv.Itoa(128<<20)) {
		t.Fatalf("after resize maxmemory is not 128 MB: %s", got[2])
	}
	if old, err := store.DeploymentByID(ctx, before.ID); err == nil && old.Status == state.DeployLive && old.TrafficPercent > 0 {
		t.Fatalf("the old deployment still serves after the handoff: %+v", old)
	}
	t.Log("step 4 PASS: resize kept the data and runs at 128 MB")
}

// waitRedisDeploymentLive waits for the cache's deployment (want, or the
// latest) to go live.
func waitRedisDeploymentLive(t *testing.T, ctx context.Context, f *privateTCPImageFixture, store *state.PgStore, appID, want string) {
	t.Helper()
	if want == "" {
		dep, err := store.LatestDeployment(ctx, appID)
		if err != nil {
			t.Fatalf("cache deployment: %v", err)
		}
		want = dep.ID
	}
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, f.pool, want, 15*time.Minute); err != nil {
		f.h.DumpLogs(t)
		t.Fatalf("cache deployment %s did not go live: %v", want, err)
	}
}

// redisMetalDo runs Redis commands from inside the caller's guest. A gateway
// refusal of the caller's HTTP request (502/503/504, or a transport error) is
// the test client's path into the guest failing, not the cache: on a loaded
// node the gateway's route lookup can miss its deadline and answer 503
// policy_unavailable (native run redis-20261117T163259). Those are retried
// for up to two minutes; any reply from the fixture itself is returned.
func redisMetalDo(t *testing.T, f *privateTCPImageFixture, host string, cmds ...string) redisMetalReply {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		got := redisMetalDoOnce(t, f, host, cmds...)
		transient := got.status == 0 || got.status == http.StatusBadGateway && got.Error == "" ||
			got.status == http.StatusServiceUnavailable || got.status == http.StatusGatewayTimeout
		if !transient || time.Now().After(deadline) {
			return got
		}
		t.Logf("redis %v: caller path refused (status=%d err=%s); retrying", cmds, got.status, got.Error)
		time.Sleep(5 * time.Second)
	}
}

func redisMetalDoOnce(t *testing.T, f *privateTCPImageFixture, host string, cmds ...string) redisMetalReply {
	t.Helper()
	query := url.Values{"host": {host}, "port": {strconv.Itoa(api.ManagedValkeyPort)}, "cmd": cmds}
	req, err := http.NewRequest(http.MethodGet, f.h.GatewayURL+"/redis?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = f.caller.Slug + ".apps.test.example"
	req.Header.Set("Authorization", "Bearer "+f.key)
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return redisMetalReply{Error: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out redisMetalReply
	if err := json.Unmarshal(body, &out); err != nil {
		out.Error = "not the fixture's response: " + string(body)
	}
	out.status = resp.StatusCode
	return out
}

// TestManagedRedisHandoffSoakMetal runs FAAS_E2E_REDIS_SOAK_HANDOFFS
// (default 100) back-to-back handoffs on one cache while a writer INCRs a
// counter through the private address the whole time, and checks after
// every handoff that no acknowledged write was lost: the counter is at
// least the number of INCRs the cache acknowledged, and the key written
// before the soak is still there. Handoffs rotate grow, shrink (with evict;
// the data is far below either size) and upgrade, so both RAM changes and
// same-RAM image moves are covered (ADR-20261116192126: resize and upgrade
// keep the data). It takes hours, so it runs only on request:
// FAAS_E2E_REDIS_SOAK=1.
func TestManagedRedisHandoffSoakMetal(t *testing.T) {
	if os.Getenv("FAAS_E2E_REDIS_SOAK") != "1" {
		t.Skip("set FAAS_E2E_REDIS_SOAK=1 to run the managed Redis handoff soak")
	}
	handoffs := 100
	if v := os.Getenv("FAAS_E2E_REDIS_SOAK_HANDOFFS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("FAAS_E2E_REDIS_SOAK_HANDOFFS=%q is not a positive count", v)
		}
		handoffs = n
	}
	f := newPrivateTCPImageFixture(t)
	if f == nil {
		return
	}
	ctx := context.Background()
	store := state.NewPgStore(f.pool)
	name := "soak-" + randHexSuffix()
	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/redis", api.CreateRedisCacheRequest{Name: name, MemoryMB: 64})
	if status != http.StatusCreated {
		t.Fatalf("create cache: status=%d body=%s", status, raw)
	}
	cacheApp, err := store.AppBySlug(ctx, name)
	if err != nil {
		t.Fatalf("load cache app: %v", err)
	}
	t.Cleanup(func() { f.parkAtCleanup(t, cacheApp) })
	waitRedisDeploymentLive(t, ctx, f, store, cacheApp.ID, "")
	index, err := store.AppServiceAddressIndex(ctx, cacheApp.ID)
	if err != nil {
		t.Fatalf("service address index: %v", err)
	}
	address, ok := api.ServiceAddressForIndex(index)
	if !ok {
		t.Fatalf("cache %s has no service address", name)
	}
	host := address.String()
	if got := redisMetalDo(t, f, host, "SET marker kept", "SET soak 0"); got.status != http.StatusOK || len(got.Replies) != 2 || got.Replies[0] != "+OK\r\n" {
		t.Fatalf("seed: status=%d replies=%q err=%s", got.status, got.Replies, got.Error)
	}

	w := startRedisSoakWriter(t, f, host)
	memory := 64
	started := time.Now()
	first, err := store.LatestDeployment(ctx, cacheApp.ID)
	if err != nil {
		t.Fatalf("cache deployment: %v", err)
	}
	deployments := []string{first.ID}
	for i := 1; i <= handoffs; i++ {
		var path, method string
		var body any
		switch i % 3 {
		case 1:
			memory, method, path = 96, http.MethodPatch, "/v1/redis/"+name
			body = api.UpdateRedisCacheRequest{MemoryMB: memory}
		case 2:
			memory, method, path = 64, http.MethodPatch, "/v1/redis/"+name
			body = api.UpdateRedisCacheRequest{MemoryMB: memory, Evict: true}
		default:
			method, path = http.MethodPost, "/v1/redis/"+name+"/upgrade"
		}
		ackedBefore := w.acked.Load()
		dep := startRedisSoakHandoff(t, f, method, path, body)
		waitRedisDeploymentLive(t, ctx, f, store, cacheApp.ID, dep)
		// Writes must resume on the new VM before the next handoff.
		deadline := time.Now().Add(3 * time.Minute)
		for w.acked.Load() <= ackedBefore+3 && time.Now().Before(deadline) {
			time.Sleep(time.Second)
		}
		if acked := w.acked.Load(); acked <= ackedBefore+3 {
			f.h.DumpLogs(t)
			t.Fatalf("handoff %d: writes did not resume within 3 min (acked %d before, %d now; last error %s)", i, ackedBefore, acked, w.lastError())
		}
		// The replaced VM is drained by schedd's reaper, up to about a
		// reaper interval after the new deployment goes live; until then a
		// new connection can still reach it, and it answers reads from its
		// pre-handoff state (its writes stay paused). Read until the reply
		// comes from the new VM (its maxmemory), then hold it to the
		// acknowledged writes counted just before that read.
		var got redisMetalReply
		var acked int64
		wantMax := "maxmemory:" + strconv.Itoa(memory<<20) + "\r\n"
		liveAt := time.Now()
		for {
			acked = w.acked.Load()
			got = redisMetalDo(t, f, host, "GET soak", "GET marker", "INFO memory")
			if got.status != http.StatusOK || len(got.Replies) != 3 {
				f.h.DumpLogs(t)
				t.Fatalf("handoff %d: read back: status=%d replies=%q err=%s", i, got.status, got.Replies, got.Error)
			}
			if strings.Contains(got.Replies[2], wantMax) {
				break
			}
			if time.Since(liveAt) > 3*time.Minute {
				f.h.DumpLogs(t)
				t.Fatalf("handoff %d: reads still reach a VM without maxmemory %d MB 3 min after the handoff went live: %s", i, memory, got.Replies[2])
			}
			time.Sleep(2 * time.Second)
		}
		if stale := time.Since(liveAt); stale > 5*time.Second {
			t.Logf("handoff %d: reads reached the replaced VM for %s after the new deployment went live", i, stale.Round(time.Second))
		}
		counter, err := strconv.ParseInt(redisBulkString(got.Replies[0]), 10, 64)
		if err != nil || counter < acked {
			f.h.DumpLogs(t)
			t.Fatalf("handoff %d: lost writes: counter=%q, acknowledged INCRs=%d", i, got.Replies[0], acked)
		}
		if got.Replies[1] != "$4\r\nkept\r\n" {
			t.Fatalf("handoff %d: marker key lost: %q", i, got.Replies[1])
		}
		t.Logf("handoff %d/%d (%s %s) PASS: counter=%d acked=%d refused=%d elapsed=%s",
			i, handoffs, method, path, counter, acked, w.refused.Load(), time.Since(started).Round(time.Second))
		deployments = append(deployments, dep)
		// Each handoff leaves its layer and snapshots behind until the
		// platform's GC reclaims retired deployments, about 1.4 GB here, so
		// 100 handoffs outgrow a test host's disk (native soak
		// redissoak-20261121T203233 stopped at 31 on ENOSPC). Reclaim a
		// deployment two handoffs after it was replaced, as GC would.
		if len(deployments) > 2 {
			pruneRetiredRedisDeployment(t, f, deployments[len(deployments)-3])
		}
	}
	w.stop()
	t.Logf("soak PASS: %d handoffs, %d acknowledged writes, none lost, %d refused while switching, %s",
		handoffs, w.acked.Load(), w.refused.Load(), time.Since(started).Round(time.Second))
}

// redisSoakWriter INCRs the soak counter as fast as one caller allows and
// counts the INCRs the cache acknowledged. A refused or failed request is
// not acknowledged, so it may or may not have applied; the check is only
// that every acknowledged one did.
type redisSoakWriter struct {
	acked   atomic.Int64
	refused atomic.Int64
	done    chan struct{}
	stopped chan struct{}
	mu      sync.Mutex
	lastErr string
}

func startRedisSoakWriter(t *testing.T, f *privateTCPImageFixture, host string) *redisSoakWriter {
	w := &redisSoakWriter{done: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(w.stopped)
		for {
			select {
			case <-w.done:
				return
			default:
			}
			got := redisMetalDoOnce(t, f, host, "INCR soak")
			if got.status == http.StatusOK && len(got.Replies) == 1 && strings.HasPrefix(got.Replies[0], ":") {
				w.acked.Add(1)
				continue
			}
			w.refused.Add(1)
			w.mu.Lock()
			w.lastErr = fmt.Sprintf("status=%d replies=%q err=%s", got.status, got.Replies, got.Error)
			w.mu.Unlock()
			time.Sleep(500 * time.Millisecond)
		}
	}()
	t.Cleanup(w.stop)
	return w
}

func (w *redisSoakWriter) stop() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	<-w.stopped
}

func (w *redisSoakWriter) lastError() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

// startRedisSoakHandoff starts one handoff and returns its deployment. The
// previous handoff's rollout may still be settling (the old VM draining), so
// a 409/429/503 refusal is retried for up to five minutes.
func startRedisSoakHandoff(t *testing.T, f *privateTCPImageFixture, method, path string, body any) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		raw, status := doReq(t, f.h, f.key, method, path, body)
		if status == http.StatusAccepted {
			var change api.RedisCacheChange
			if err := json.Unmarshal(raw, &change); err != nil || change.DeploymentID == "" {
				t.Fatalf("%s %s: response without a deployment: %s", method, path, raw)
			}
			return change.DeploymentID
		}
		retry := status == http.StatusConflict || status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
		if !retry || time.Now().After(deadline) {
			f.h.DumpLogs(t)
			t.Fatalf("%s %s: status=%d body=%s", method, path, status, raw)
		}
		t.Logf("%s %s refused (%d %s); retrying", method, path, status, raw)
		time.Sleep(10 * time.Second)
	}
}

// pruneRetiredRedisDeployment deletes a retired deployment's artifacts: every
// path named after its ID under the harness's storage root, and vmmd's local
// snapshot directory for it. The ID is a UUID, so nothing else matches.
func pruneRetiredRedisDeployment(t *testing.T, f *privateTCPImageFixture, deploymentID string) {
	t.Helper()
	var doomed []string
	_ = filepath.WalkDir(f.h.TmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.Contains(d.Name(), deploymentID) {
			doomed = append(doomed, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	doomed = append(doomed, filepath.Join("/srv/fc/snap", deploymentID))
	for _, path := range doomed {
		if err := os.RemoveAll(path); err != nil {
			t.Logf("prune retired deployment %s: %v", deploymentID, err)
		}
	}
}

// redisBulkString is a RESP bulk string's value ("$2\r\n42\r\n" -> "42").
func redisBulkString(reply string) string {
	_, rest, ok := strings.Cut(reply, "\r\n")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(rest, "\r\n")
}
