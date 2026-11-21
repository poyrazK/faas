package main_test

import (
	"bytes"
	"encoding/json"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestCmdRedisListJSON pins `gregale redis list --json`: the API response
// is printed as JSON, not the table.
func TestCmdRedisListJSON(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = false })
	f := main.AuthedFakeAPI(t, `{"caches":[{"name":"sessions","memory_mb":256,"eviction_policy":"allkeys-lru","host":"sessions.redis.internal","port":6379}],"limit":1}`, 200)
	var out bytes.Buffer
	if code := main.CaptureStdoutSwap(t, &out, func() int { return main.CmdRedis([]string{"list"}) }); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if (*f.SawMethod()) != "GET" || (*f.SawPath()) != "/v1/redis" {
		t.Fatalf("route = %s %s, want GET /v1/redis", *f.SawMethod(), *f.SawPath())
	}
	var got struct {
		Caches []struct {
			Name string `json:"name"`
		} `json:"caches"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if len(got.Caches) != 1 || got.Caches[0].Name != "sessions" || got.Limit != 1 {
		t.Fatalf("output = %+v", got)
	}
}
