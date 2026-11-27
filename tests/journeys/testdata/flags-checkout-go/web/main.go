// The Go checkout probe of the Flags SDK journey (tests/journeys,
// TestFlagsSDKs/go). It builds against the repository's Go SDK, which the
// journey copies in as ./gregale-go (go.mod replaces the module with it),
// and reads the API URL the journey writes to config.json. The same contract
// as the Node probe (flags-checkout):
//
//	GET /                    {"probe", "customer"}: the verified customer
//	GET /debug               why the SDK has no configuration: the refresh
//	                         error and the identity and runtime statuses
//	GET /checkout            checks the "checkout" variant flag, marks it used
//	GET /checkout?convert=1  also tracks an order_completed conversion
//
// The SDK's Middleware pins one configuration per request and returns the
// request's flag evidence, so the gateway records the exposure.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gregale-dev/gregale-go/preview"
)

type config struct {
	APIURL string `json:"apiURL"`
}

func main() {
	raw, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatal(err)
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Fatal(err)
	}
	flags, err := preview.NewGregaleFlags(preview.GregaleFlagsOptions{
		APIURL: cfg.APIURL, RefreshInterval: 2 * time.Second, MaxStale: 10 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := flags.Start(context.Background()); err != nil {
		log.Printf("flags start: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		send(w, debug(r.Context(), flags, cfg.APIURL))
	})
	mux.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		decision, _ := flags.Variant(ctx, "checkout", "fallback")
		_ = flags.Used(ctx, "checkout")
		tracked := false
		if r.URL.Query().Get("convert") == "1" {
			tracked = flags.Track(ctx, "order_completed", 1200)
		}
		_ = flags.Flush(ctx)
		send(w, map[string]any{
			"probe": "gregale-e2e-flags", "customer": r.Header.Get("X-Faas-Platform-Tenant-Id"),
			"variant": decision.Value, "reason": decision.Reason, "rule_id": decision.RuleID,
			"config_version": decision.ConfigVersion, "tracked": tracked, "dropped": flags.DroppedEvents(),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"probe": "gregale-e2e-flags", "customer": r.Header.Get("X-Faas-Platform-Tenant-Id")})
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Fatal(http.ListenAndServe(":"+port, flags.Middleware(mux)))
}

func send(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// debug reports what the SDK's refresh sees, without the token.
func debug(ctx context.Context, flags *preview.GregaleFlags, apiURL string) map[string]any {
	endpoint := os.Getenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT")
	out := map[string]any{"identity_endpoint": endpoint != ""}
	if err := flags.Refresh(ctx); err != nil {
		out["refresh"] = err.Error()
	} else {
		out["refresh"] = "ok"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	id, err := url.Parse(endpoint)
	if err != nil || endpoint == "" {
		out["probe_error"] = "no identity endpoint"
		return out
	}
	q := id.Query()
	q.Set("audience", "gregale:flags")
	id.RawQuery = q.Encode()
	resp, err := client.Get(id.String())
	if err != nil {
		out["probe_error"] = err.Error()
		return out
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	resp.Body.Close()
	out["identity_status"] = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		out["identity_body"] = string(body)
		return out
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(body, &token)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+"/v1/runtime/flags", nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	fr, err := client.Do(req)
	if err != nil {
		out["probe_error"] = err.Error()
		return out
	}
	runtime, _ := io.ReadAll(io.LimitReader(fr.Body, 400))
	fr.Body.Close()
	out["runtime_status"], out["runtime_body"] = fr.StatusCode, string(runtime)
	return out
}
