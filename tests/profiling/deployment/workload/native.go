package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/google/pprof/profile"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var nativeProbe struct {
	sync.Mutex
	bootID string
	epoch  string
	body   []byte
	from   int64
}
var epochPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Enable only in the disposable fixture. The bridge destination is fixed to
// guest loopback; clients cannot supply a forwarding URL or profile payload.
func installNativeProbe(mux *http.ServeMux) {
	token := os.Getenv("GREGALE_NATIVE_PROFILE_TOKEN")
	if len(token) < 32 {
		return
	}
	nativeProbe.bootID = uuid.NewString()
	protect := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			want := "Bearer " + token
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next(w, r)
		}
	}
	mux.HandleFunc("GET /acceptance/profile-state", protect(nativeProfileState))
	mux.HandleFunc("POST /acceptance/stale-profile", protect(nativeStaleProfile))
}

func bridgeControl(r *http.Request) (string, error) {
	req, err := http.NewRequestWithContext(r.Context(), "GET", api.ProfileLocalEndpoint+"/control", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: api.ProfileTransportTimeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var c struct {
		Epoch     string `json:"epoch"`
		Enabled   bool   `json:"enabled"`
		Suspended bool   `json:"suspended"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bridge status")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, api.ProfileControlMaxBytes)).Decode(&c); err != nil {
		return "", err
	}
	if !c.Enabled || c.Suspended || !epochPattern.MatchString(c.Epoch) {
		return "", fmt.Errorf("bridge not active")
	}
	return c.Epoch, nil
}

// Stage a valid CPU-format probe before park. It is never admitted as telemetry:
// replay is permitted only after the bridge reports a different current epoch.
func nativeProfileState(w http.ResponseWriter, r *http.Request) {
	epoch, err := bridgeControl(r)
	if err != nil {
		http.Error(w, "bridge unavailable", http.StatusServiceUnavailable)
		return
	}
	nativeProbe.Lock()
	defer nativeProbe.Unlock()
	if nativeProbe.body == nil {
		now := time.Now()
		f := &profile.Function{ID: 1, Name: "nativeAcceptanceStaleProbe"}
		l := &profile.Location{ID: 1, Line: []profile.Line{{Function: f}}}
		p := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
			TimeNanos: now.UnixNano(), DurationNanos: int64(time.Millisecond),
			Function: []*profile.Function{f}, Location: []*profile.Location{l},
			Sample: []*profile.Sample{{Location: []*profile.Location{l}, Value: []int64{1000000}}}}
		var body bytes.Buffer
		if err := p.Write(&body); err != nil {
			http.Error(w, "probe encoding", http.StatusInternalServerError)
			return
		}
		nativeProbe.body, nativeProbe.epoch, nativeProbe.from = body.Bytes(), epoch, now.UnixNano()
	}
	digest := sha256.Sum256(nativeProbe.body)
	_ = json.NewEncoder(w).Encode(map[string]any{"boot_id": nativeProbe.bootID, "epoch": epoch,
		"staged_epoch": nativeProbe.epoch, "probe_sha256": hex.EncodeToString(digest[:]), "requests": requests.Load()})
}

func nativeStaleProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Epoch string `json:"epoch"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProfileControlMaxBytes)).Decode(&input); err != nil || !epochPattern.MatchString(input.Epoch) {
		http.Error(w, "invalid epoch", http.StatusBadRequest)
		return
	}
	current, err := bridgeControl(r)
	if err != nil {
		http.Error(w, "bridge unavailable", http.StatusServiceUnavailable)
		return
	}
	nativeProbe.Lock()
	defer nativeProbe.Unlock()
	if nativeProbe.body == nil || input.Epoch != nativeProbe.epoch || current == input.Epoch {
		http.Error(w, "probe requires a retained old epoch", http.StatusConflict)
		return
	}
	values := url.Values{"name": {fmt.Sprintf("gregale{gregale_epoch=\"%s\",gregale_process=\"%d\"}", input.Epoch, os.Getpid())},
		"from": {fmt.Sprint(nativeProbe.from / 1e9)}, "until": {fmt.Sprint((nativeProbe.from + int64(time.Millisecond)) / 1e9)}}
	req, err := http.NewRequestWithContext(r.Context(), "POST", api.ProfileLocalEndpoint+"/ingest?"+values.Encode(), bytes.NewReader(nativeProbe.body))
	if err != nil {
		http.Error(w, "probe request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	client := &http.Client{Timeout: api.ProfileTransportTimeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "bridge unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.ProfileControlMaxBytes))
	rejected := err == nil && resp.StatusCode == http.StatusConflict && bytes.Contains(body, []byte("profile belongs to an earlier collection epoch"))
	digest := sha256.Sum256(nativeProbe.body)
	_ = json.NewEncoder(w).Encode(map[string]any{"stale_capture_rejected": rejected, "bridge_status": resp.StatusCode, "probe_sha256": hex.EncodeToString(digest[:])})
}
