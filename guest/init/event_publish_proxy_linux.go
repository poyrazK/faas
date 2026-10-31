//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// EventPublishEndpoint (metadata_port.go) is the in-guest endpoint
// applications use to publish an internal event without carrying a
// control-plane credential. guest-init forwards the request over the
// instance-bound Firecracker vsock; vmmd supplies the tenant identity from its
// live-instance map.
const (
	eventPublishType    = byte(0x07)
	eventPublishMaxBody = 64 << 10
	// eventPublishVerdictType asks vmmd for its verdict (ADR-20261031102000):
	// a 4-byte big-endian body length, the body, then vmmd's reply.
	eventPublishVerdictType = byte(0x0B)
	// eventPublishVerdictTimeout stays under the proxy's 5 s write timeout,
	// so an unanswered publish still gets a 503 back to the app.
	eventPublishVerdictTimeout = 4 * time.Second
)

// eventPublishVerdict is set when the host vmmd answers publishes with a
// verdict: from the boot command line on cold boot, and from each resume
// request after a restore.
var eventPublishVerdict atomic.Bool

// startEventPublishProxy exposes the metadata-style event ingress inside the
// guest. The listener is restricted to the loopback-local link address; the
// workload still cannot reach another VM's vsock because Firecracker
// terminates the channel per instance.
func startEventPublishProxy(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if err := configureMetadataLoopback(); err != nil {
		return err
	}
	// Leave port 80 to a workload that serves it; see metadata_port.go.
	setMetadataProxyPort(metadataProxyPort(metadataWorkloadTCPPorts(os.DirFS("/"))))
	ln, err := net.Listen("tcp4", metadataListenAddr)
	if err != nil {
		return fmt.Errorf("event publish proxy listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(metadataEnvPath, metadataEnvHandler)
	mux.HandleFunc(metadataSecretReloadAckPath, metadataSecretReloadAckHandler)
	mux.HandleFunc(crashCapturePath, crashCaptureHandler)
	eventPublishVerdict.Store(bootArgSet(api.EventPublishVerdictBootArg))
	mux.HandleFunc(eventPublishPath, func(w http.ResponseWriter, r *http.Request) {
		handleEventPublishRequest(w, r, sendEventPublish)
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

// PID1 can have an empty PATH, and full-rootfs app images need not ship ip.
// Configure only the guest's loopback directly, keeping the listener local.
func configureMetadataLoopback() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("metadata loopback socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := setLoopbackUp(fd); err != nil {
		return fmt.Errorf("metadata %w", err)
	}
	// An alias preserves lo's primary 127.0.0.1 address and makes repeated
	// setup idempotent. Its /32 mask creates only the platform metadata route.
	if err := setMetadataLoopbackIPv4(fd, unix.SIOCSIFADDR, []byte{169, 254, 169, 254}); err != nil {
		return err
	}
	return setMetadataLoopbackIPv4(fd, unix.SIOCSIFNETMASK, []byte{255, 255, 255, 255})
}

// bringUpLoopback sets lo up, which also gives it 127.0.0.1. Networked
// guests get this from their network setup; networkless agent run VMs call
// it before serving the relay.
func bringUpLoopback() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("loopback socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	return setLoopbackUp(fd)
}

func setLoopbackUp(fd int) error {
	loopback, err := unix.NewIfreq("lo")
	if err != nil {
		return fmt.Errorf("loopback interface: %w", err)
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, loopback); err != nil {
		return fmt.Errorf("loopback flags: %w", err)
	}
	loopback.SetUint16(loopback.Uint16() | unix.IFF_UP)
	if err := unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, loopback); err != nil {
		return fmt.Errorf("loopback enable: %w", err)
	}
	return nil
}

func setMetadataLoopbackIPv4(fd int, operation uint, address []byte) error {
	request, err := unix.NewIfreq("lo:faas")
	if err != nil {
		return fmt.Errorf("metadata loopback alias: %w", err)
	}
	if err := request.SetInet4Addr(address); err != nil {
		return fmt.Errorf("metadata loopback IPv4: %w", err)
	}
	if err := unix.IoctlIfreq(fd, operation, request); err != nil {
		return fmt.Errorf("metadata loopback address: %w", err)
	}
	return nil
}

// sendEventPublish forwards one event to vmmd. Under a vmmd that answers,
// the app gets vmmd's verdict; under an older one the frame is fire-and-forget
// and the endpoint answers 202 once it is handed over, as before.
func sendEventPublish(body []byte) (api.EventPublishVerdict, error) {
	if eventPublishVerdict.Load() {
		return sendGuestEventPublishForVerdict(body, eventPublishVerdictTimeout)
	}
	frame := make([]byte, 1+len(body))
	frame[0] = eventPublishType
	copy(frame[1:], body)
	if err := sendGuestEventFrame(frame, 2*time.Second); err != nil {
		return api.EventPublishVerdict{}, err
	}
	return api.EventPublishVerdict{Status: http.StatusAccepted}, nil
}

func handleEventPublishRequest(w http.ResponseWriter, r *http.Request, send func([]byte) (api.EventPublishVerdict, error)) {
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
	verdict, err := send(body)
	if err != nil {
		// Unconfirmed: vmmd has the event but no answer came back, so it may
		// have been accepted. Publishing it again with the same id is safe.
		code := "event_publish_unavailable"
		if errors.Is(err, errEventPublishUnconfirmed) {
			code = "event_publish_unconfirmed"
		}
		w.Header().Set("Retry-After", "1")
		writeEventPublishError(w, http.StatusServiceUnavailable, code)
		return
	}
	if verdict.Status == http.StatusAccepted {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"accepted":true}`)
		return
	}
	status, code := verdict.Status, verdict.Code
	if status < 400 || status > 599 {
		status = http.StatusServiceUnavailable
	}
	if code == "" {
		code = "event_publish_refused"
	}
	if verdict.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(verdict.RetryAfterSeconds))
	}
	writeEventPublishError(w, status, code)
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
