// adr: 638
package faas_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// Opt-in acceptance against the deployed example; no fixture substitution.
// All writes use a unique retained entity key in the explicitly selected app.
func TestDurableEntityDeployedCounterAcceptance(t *testing.T) {
	if os.Getenv("GREGALE_ENTITY_API_QUALIFY") != "1" {
		t.Skip("set GREGALE_ENTITY_API_QUALIFY=1 for the deployed counter preview")
	}
	base, token, app := os.Getenv("GREGALE_API_URL"), os.Getenv("GREGALE_API_TOKEN"), os.Getenv("GREGALE_ENTITY_APP_SLUG")
	if base == "" || token == "" || app == "" {
		t.Fatal("set API URL, account token and explicit counter app slug")
	}
	client, err := faas.NewClient(base, token, faas.WithRetry(6, 200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	request := faas.DurableEntityInvokeRequest{Namespace: "counters", Key: "qualification:" + hex.EncodeToString(random), RequestID: "first", Payload: json.RawMessage(`{"delta":1}`), Environment: os.Getenv("GREGALE_ENTITY_ENVIRONMENT"), PlatformTenantID: os.Getenv("GREGALE_ENTITY_TENANT_ID")}
	started := time.Now()
	first, err := client.InvokeDurableEntity(ctx, app, request)
	if err != nil || first.Version != 1 || string(first.Value) != `{"count":1}` {
		t.Fatalf("first deployed result = %+v %v", first, err)
	}
	const calls = 8
	var wg sync.WaitGroup
	versions := make(chan uint64, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := request
			req.RequestID = "concurrent-" + string(rune('a'+i))
			result, err := client.InvokeDurableEntity(ctx, app, req)
			if err != nil || result.Version < 2 || result.Version > calls+1 {
				t.Errorf("concurrent deployed result = %+v %v", result, err)
				return
			}
			versions <- result.Version
		}(i)
	}
	wg.Wait()
	close(versions)
	seen := map[uint64]bool{}
	for version := range versions {
		if seen[version] {
			t.Errorf("different request IDs committed the same version %d", version)
		}
		seen[version] = true
	}
	if len(seen) != calls {
		t.Fatalf("only %d/%d concurrent requests acknowledged", len(seen), calls)
	}
	replayed, err := client.InvokeDurableEntity(ctx, app, request)
	if err != nil || !replayed.Replayed || replayed.Version != first.Version || string(replayed.Value) != string(first.Value) {
		t.Fatalf("deployed replay changed original result = %+v %v", replayed, err)
	}
	request.Payload = json.RawMessage(`{"delta":2}`)
	_, err = client.InvokeDurableEntity(ctx, app, request)
	problem, ok := faas.AsAPIError(err)
	if !ok || problem.Problem.Code != "durable_entity_request_conflict" {
		t.Fatal("reusing a request ID with another payload did not fail with the expected conflict")
	}
	request.RequestID, request.Payload = "final", json.RawMessage(`{"delta":1}`)
	final, err := client.InvokeDurableEntity(ctx, app, request)
	var value struct {
		Count int `json:"count"`
	}
	if err != nil || json.Unmarshal(final.Value, &value) != nil || final.Version != calls+2 || value.Count != calls+2 {
		t.Fatalf("deployed counter lost or duplicated updates = %+v %v", final, err)
	}
	t.Logf("retained entity key=%s; calls=%d; elapsed_ms=%d", request.Key, calls+4, time.Since(started).Milliseconds())
	if os.Getenv("GREGALE_ENTITY_ALARMS_QUALIFY") == "1" {
		qualifyDeployedEntityAlarm(t, ctx, client, app, request)
	}
}

func qualifyDeployedEntityAlarm(t *testing.T, ctx context.Context, client *faas.Client, app string, request faas.DurableEntityInvokeRequest) {
	t.Helper()
	request.Key += ":alarm"
	request.RequestID = "schedule-alarm"
	request.Payload, _ = json.Marshal(struct {
		Delta int       `json:"delta"`
		At    time.Time `json:"alarm_at"`
	}{0, time.Now().UTC().Add(time.Second)})
	first, err := client.InvokeDurableEntity(ctx, app, request)
	if err != nil || first.Version != 1 || string(first.Value) != `{"count":0}` {
		t.Fatalf("deployed alarm schedule = %+v %v", first, err)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	observed := request
	observed.Payload = json.RawMessage(`{"delta":0}`)
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			t.Fatal("deployed alarm was not acknowledged before the qualification deadline")
		case <-ticker.C:
		}
		observed.RequestID = "observe-alarm-" + fmt.Sprint(i)
		result, err := client.InvokeDurableEntity(ctx, app, observed)
		var value struct {
			Count int `json:"count"`
		}
		if err != nil || json.Unmarshal(result.Value, &value) != nil || value.Count < 0 || value.Count > 1 {
			t.Fatalf("deployed alarm lost or duplicated state = %+v %v", result, err)
		}
		if value.Count == 1 {
			break
		}
	}
	replayed, err := client.InvokeDurableEntity(ctx, app, request)
	if err != nil || !replayed.Replayed || replayed.Version != first.Version || string(replayed.Value) != string(first.Value) {
		t.Fatalf("alarm changed original request receipt = %+v %v", replayed, err)
	}
	t.Logf("deployed alarm acknowledged; retained entity key=%s", request.Key)
}
