//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"time"
)

// EventPublishEndpoint is the stable in-guest endpoint applications use to
// publish an internal event without carrying a control-plane credential.
// guest-init forwards the request over the instance-bound Firecracker vsock;
// vmmd supplies the tenant identity from its live-instance map.
const EventPublishEndpoint = "http://169.254.169.254/v1/events:publish"

const (
	eventPublishListenAddr = "169.254.169.254:80"
	eventPublishPath       = "/v1/events:publish"
	eventPublishType       = byte(0x07)
	eventPublishMaxBody    = 64 << 10
)

// startEventPublishProxy exposes the metadata-style event ingress inside the
// guest. The listener is restricted to the loopback-local link address; the
// workload still cannot reach another VM's vsock because Firecracker
// terminates the channel per instance.
func startEventPublishProxy(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// The metadata address is loopback-local inside the VM. BusyBox images
	// normally ship `ip`; if a minimal image does not, fail closed rather than
	// binding a wildcard address and exposing the platform endpoint externally.
	if err := exec.Command("ip", "addr", "add", "169.254.169.254/32", "dev", "lo").Run(); err != nil {
		log.Debug("event publish metadata address setup skipped", "err", err)
	}
	ln, err := net.Listen("tcp4", eventPublishListenAddr)
	if err != nil {
		return fmt.Errorf("event publish proxy listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(metadataEnvPath, metadataEnvHandler)
	mux.HandleFunc(eventPublishPath, func(w http.ResponseWriter, r *http.Request) {
		handleEventPublishRequest(w, r, func(body []byte) error {
			frame := make([]byte, 1+len(body))
			frame[0] = eventPublishType
			copy(frame[1:], body)
			return sendGuestEventFrame(frame, 2*time.Second)
		})
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("event publish proxy stopped", "err", err)
		}
	}()
	log.Info("event publish proxy started", "endpoint", EventPublishEndpoint, "metadata_endpoint", metadataEnvEndpoint)
	return nil
}

func handleEventPublishRequest(w http.ResponseWriter, r *http.Request, send func([]byte) error) {
	if r.Method != http.MethodPost {
		writeEventPublishError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if send == nil {
		writeEventPublishError(w, http.StatusServiceUnavailable, "event_publish_unavailable")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, eventPublishMaxBody+1))
	if err != nil {
		writeEventPublishError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if len(body) == 0 || len(body) > eventPublishMaxBody || !json.Valid(body) {
		writeEventPublishError(w, http.StatusBadRequest, "invalid_event")
		return
	}
	if err := send(body); err != nil {
		writeEventPublishError(w, http.StatusServiceUnavailable, "event_publish_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"accepted":true}`)
}

func writeEventPublishError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// StampEventPublishEnv exposes the endpoint without allowing a customer
// manifest or deployment override to redirect the platform-owned URL.
func StampEventPublishEnv(env []string) []string {
	return append(env, "FAAS_EVENT_PUBLISH_ENDPOINT="+EventPublishEndpoint)
}
