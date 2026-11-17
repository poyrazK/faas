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
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
