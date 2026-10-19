//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

type profileControl struct {
	Enabled       bool   `json:"enabled"`
	Suspended     bool   `json:"suspended"`
	Epoch         string `json:"epoch"`
	WindowSeconds int    `json:"window_seconds"`
	// Kinds lists the profile kinds to collect; empty means CPU only so
	// collectors that predate ADR-1155 keep their behavior.
	Kinds []string `json:"kinds,omitempty"`
	// Capture marks an on-demand window. Collectors stop at the window end
	// rather than starting the next continuous window.
	Capture bool `json:"capture,omitempty"`
	// CheckpointSeconds asks heap collectors in a capture for interim
	// profiles (?checkpoint=1) so an OOM kill still leaves the last heap.
	CheckpointSeconds int `json:"checkpoint_seconds,omitempty"`
}

// profileCapture is one armed on-demand window (ADR-1155).
type profileCapture struct {
	request   profileproto.CaptureRequest
	epoch     string
	profiles  []profileproto.CapturedProfile
	bytes     int
	dropped   int
	processes map[string]bool
	aborted   string
	// checkpoints holds the latest interim heap profile per process, used
	// for processes that never deliver a final one (OOM-killed).
	checkpoints     map[string]profileproto.CapturedProfile
	checkpointBytes int
	// stop ends the window early; stopReason explains it in the reply.
	stop       chan struct{}
	stopReason string
	// oomKilled marks a window ended by an OOM kill of the workload.
	oomKilled bool
	oomPeakMB int
	// scrapeNote explains a profile guest-init could not collect from the
	// application's pprof endpoint (profiling.pprof_port); probeNote explains
	// an endpoint that never answered, and applies only with no processes.
	scrapeNote, probeNote string
	// replied closes once the reply has been written to the host.
	replied     chan struct{}
	repliedOnce sync.Once
}

func (c *profileCapture) markReplied() {
	if c != nil && c.replied != nil {
		c.repliedOnce.Do(func() { close(c.replied) })
	}
}

type profileBridge struct {
	mu sync.Mutex
	// base is the deployment's continuous configuration. Enabled is false
	// for a dormant on-demand bridge.
	base    profileControl
	control profileControl
	capture *profileCapture
	// finished is the last capture whose reply may still be in flight.
	finished  *profileCapture
	processes map[string]time.Time
	acks      map[string]string
	slots     chan struct{}
	send      func(context.Context, profileproto.Upload) error
	// pprofSeen and pprofAck are the pprof scraper's poll and suspend
	// acknowledgement; it registers here rather than in processes so a
	// collector of the application's own can be told apart from it.
	pprofSeen time.Time
	pprofAck  string
}

var guestProfiles struct {
	sync.Mutex
	bridge *profileBridge
}
var profileEpochLabel = regexp.MustCompile(`(?:^|[,{])gregale_epoch="?([a-f0-9]{32})"?(?:[,}]|$)`)
var profileProcessLabel = regexp.MustCompile(`(?:^|[,{])gregale_process="?([0-9]+)"?(?:[,}]|$)`)

func newProfileEpoch() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(raw[:])
}

func newProfileBridge(cfg *api.ProfilingConfig, send func(context.Context, profileproto.Upload) error) *profileBridge {
	base := profileControl{Enabled: cfg != nil && cfg.Enabled, WindowSeconds: cfg.EffectiveWindowSeconds()}
	if base.Enabled {
		base.Kinds = cfg.EffectiveKinds()
	}
	control := base
	control.Epoch = newProfileEpoch()
	return &profileBridge{base: base, control: control, processes: map[string]time.Time{}, acks: map[string]string{}, slots: make(chan struct{}, api.ProfileMaxConcurrentUploads), send: send}
}

// startProfileBridge serves the local collector bridge when continuous
// profiling is enabled or when the deployment allows on-demand captures. A
// dormant bridge answers enabled=false until a capture arms it.
func startProfileBridge(cfg *api.ProfilingConfig, onDemand bool, log *slog.Logger) error {
	if (cfg == nil || !cfg.Enabled) && !onDemand {
		return nil
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:9191")
	if err != nil {
		return fmt.Errorf("profile bridge listen: %w", err)
	}
	bridge := newProfileBridge(cfg, sendProfileUpload)
	guestProfiles.Lock()
	guestProfiles.bridge = bridge
	guestProfiles.Unlock()
	srv := &http.Server{Handler: bridge.handler(), ReadHeaderTimeout: time.Second, ReadTimeout: api.ProfileTransportTimeout, WriteTimeout: api.ProfileTransportTimeout}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("profile bridge stopped")
		}
	}()
	return nil
}

func (b *profileBridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /control", b.controlHandler)
	mux.HandleFunc("POST /control/ack", b.ackHandler)
	mux.HandleFunc("POST /ingest", b.ingestHandler)
	mux.HandleFunc("POST /push.v1.PusherService/Push", b.pushHandler)
	return mux
}

func validProfilePID(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n < 1<<22
}

func (b *profileBridge) controlHandler(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if pid := r.URL.Query().Get("pid"); validProfilePID(pid) {
		for key, at := range b.processes {
			if time.Since(at) > api.ProfileProcessStaleAfter {
				delete(b.processes, key)
				delete(b.acks, key)
			}
		}
		if _, ok := b.processes[pid]; ok || len(b.processes) < api.ProfileMaxProcesses {
			b.processes[pid] = time.Now()
			if b.capture != nil && b.control.Epoch == b.capture.epoch && len(b.capture.processes) < api.ProfileMaxProcesses {
				b.capture.processes[pid] = true
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(b.control)
}

func (b *profileBridge) ackHandler(w http.ResponseWriter, r *http.Request) {
	pid, epoch := r.URL.Query().Get("pid"), r.URL.Query().Get("epoch")
	b.mu.Lock()
	defer b.mu.Unlock()
	if !validProfilePID(pid) || epoch != b.control.Epoch {
		http.Error(w, "invalid acknowledgement", http.StatusBadRequest)
		return
	}
	if _, ok := b.processes[pid]; ok {
		b.acks[pid] = epoch
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *profileBridge) ingestHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		http.Error(w, "profile bridge busy", http.StatusTooManyRequests)
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = api.ProfileKindCPU
	}
	if !api.ValidCaptureProfileKind(kind) {
		http.Error(w, "unknown profile kind", http.StatusBadRequest)
		return
	}
	checkpoint := r.URL.Query().Get("checkpoint") == "1"
	if checkpoint && kind != api.ProfileKindHeap {
		http.Error(w, "only heap profiles have checkpoints", http.StatusBadRequest)
		return
	}
	match := profileEpochLabel.FindStringSubmatch(r.URL.Query().Get("name"))
	b.mu.Lock()
	epoch := b.control.Epoch
	b.mu.Unlock()
	if len(match) != 2 || match[1] != epoch {
		http.Error(w, "profile belongs to an earlier collection epoch", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.ProfileMaxFrameBytes)
	body, err := profileUploadBody(r)
	if err != nil || len(body) == 0 || len(body) > api.ProfileMaxCompressedBytes {
		http.Error(w, "profile exceeds bounds or has invalid encoding", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProfileTransportTimeout)
	defer cancel()
	report, err := profileproto.DecodeRouteRequestHeader(r.Header.Get(profileproto.RouteRequestHeader))
	if err != nil {
		report = nil
	} // Optional counters cannot discard otherwise valid CPU.
	upload := profileproto.Upload{RouteRequests: report, Profile: body, FromUnixNano: profileQueryTime(r.URL.Query(), "from"), UntilUnixNano: profileQueryTime(r.URL.Query(), "until")}
	if kind != api.ProfileKindCPU {
		upload.Kind = kind
	}
	if match := profileProcessLabel.FindStringSubmatch(r.URL.Query().Get("name")); len(match) == 2 {
		upload.ProcessID = match[1]
	}
	if checkpoint {
		if !b.acceptCheckpoint(epoch, upload) {
			http.Error(w, "no capture is collecting checkpoints", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := b.accept(ctx, epoch, upload); err != nil {
		http.Error(w, "profile collection unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// acceptCheckpoint keeps the latest interim heap profile per process for the
// running capture. Checkpoints share the reply's byte budget, so a guest
// already short on memory never holds more than one reply's worth.
func (b *profileBridge) acceptCheckpoint(epoch string, upload profileproto.Upload) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.capture
	if c == nil || c.epoch != epoch || c.request.Checkpoint() == 0 || !c.request.WantsKind(api.ProfileKindHeap) {
		return false
	}
	key := api.ProfileKindHeap + "/" + upload.ProcessID
	previous := len(c.checkpoints[key].Profile)
	if len(c.checkpoints) >= api.ProfileMaxProcesses && previous == 0 || c.checkpointBytes-previous+len(upload.Profile) > profileproto.CaptureMaxProfileBytes {
		return true // keep the earlier checkpoint; the upload itself was valid
	}
	c.checkpoints[key] = profileproto.CapturedProfile{Kind: api.ProfileKindHeap, ProcessID: upload.ProcessID, Profile: append([]byte(nil), upload.Profile...),
		FromUnixNano: upload.FromUnixNano, UntilUnixNano: upload.UntilUnixNano}
	c.checkpointBytes += len(upload.Profile) - previous
	return true
}

// accept routes one upload. Uploads in a capture epoch are retained for the
// capture reply and also forwarded when continuous collection covers that
// kind, so an on-demand window does not leave a continuous gap.
func (b *profileBridge) accept(ctx context.Context, epoch string, upload profileproto.Upload) error {
	kind := upload.Kind
	if kind == "" {
		kind = api.ProfileKindCPU
	}
	b.mu.Lock()
	captured := false
	if c := b.capture; c != nil && c.epoch == epoch {
		captured = true
		if c.request.WantsKind(kind) {
			if len(c.profiles) >= profileproto.CaptureMaxProfiles || c.bytes+len(upload.Profile) > profileproto.CaptureMaxProfileBytes {
				c.dropped++
			} else {
				c.profiles = append(c.profiles, profileproto.CapturedProfile{Kind: kind, ProcessID: upload.ProcessID, Profile: append([]byte(nil), upload.Profile...), FromUnixNano: upload.FromUnixNano, UntilUnixNano: upload.UntilUnixNano})
				c.bytes += len(upload.Profile)
			}
		}
	}
	forward := b.base.Enabled && slices.Contains(b.base.Kinds, kind)
	b.mu.Unlock()
	if !forward {
		if captured {
			return nil
		}
		return fmt.Errorf("profile kind is not collected")
	}
	return b.send(ctx, upload)
}

func profileUploadBody(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		reader, err := r.MultipartReader()
		if err != nil {
			return nil, err
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if part.FormName() == "profile" {
				return io.ReadAll(io.LimitReader(part, api.ProfileMaxCompressedBytes+1))
			}
			_, _ = io.Copy(io.Discard, part)
		}
		return nil, fmt.Errorf("missing profile")
	}
	return io.ReadAll(io.LimitReader(r.Body, api.ProfileMaxCompressedBytes+1))
}

func profileQueryTime(q url.Values, key string) int64 {
	n, err := strconv.ParseInt(q.Get(key), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	// Pyroscope ingestion parameters use Unix seconds.
	if n > int64(^uint64(0)>>1)/int64(time.Second) {
		return 0
	}
	return n * int64(time.Second)
}

func (b *profileBridge) checkpoint() {
	b.mu.Lock()
	b.control.Suspended = true
	if b.capture != nil {
		b.capture.aborted = "instance checkpointed during capture"
	}
	// A dormant bridge has no running collector to drain.
	dormant := !b.control.Enabled && b.capture == nil
	b.mu.Unlock()
	if dormant {
		return
	}
	deadline := time.Now().Add(api.ProfileDrainTimeout)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		done := true
		for pid, at := range b.processes {
			if time.Since(at) < api.ProfileProcessStaleAfter && b.acks[pid] != b.control.Epoch {
				done = false
			}
		}
		if time.Since(b.pprofSeen) < api.ProfileProcessStaleAfter && b.pprofAck != b.control.Epoch {
			done = false
		}
		b.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pauseGuestProfiles() bool {
	guestProfiles.Lock()
	b := guestProfiles.bridge
	guestProfiles.Unlock()
	if b != nil {
		b.checkpoint()
		return true
	}
	return false
}
func resumeGuestProfiles() {
	guestProfiles.Lock()
	b := guestProfiles.bridge
	guestProfiles.Unlock()
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capture != nil && b.capture.aborted == "" {
		b.capture.aborted = "instance restored during capture"
	}
	b.capture = nil
	b.control = b.base
	b.control.Epoch = newProfileEpoch()
	b.processes = map[string]time.Time{}
	b.acks = map[string]string{}
	b.pprofSeen, b.pprofAck = time.Time{}, ""
}

func sendProfileUpload(ctx context.Context, upload profileproto.Upload) error {
	deadline := time.Now().Add(api.ProfileTransportTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn, err := dialRuntimeConfigVsock(api.ProfileVsockPort, time.Until(deadline))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	body, err := json.Marshal(upload)
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := io.Copy(conn, strings.NewReader(string(header[:])+string(body))); err != nil {
		return err
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return err
	}
	if ack[0] != 0 {
		return fmt.Errorf("host declined profile")
	}
	return nil
}

func stampProfileEnv(env []string, cfg *api.ProfilingConfig, onDemand bool) []string {
	return profileEnvAtPaths(env, cfg, onDemand, api.ProfileNodeBootstrapPath, api.ProfilePythonBootstrapDir, func(path string) bool { _, err := os.Stat(path); return err == nil })
}

func profileEnvAtPaths(env []string, cfg *api.ProfilingConfig, onDemand bool, nodePath, pythonPath string, exists func(string) bool) []string {
	if (cfg == nil || !cfg.Enabled) && !onDemand {
		return env
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, ok := cut(entry)
		if ok {
			values[key] = value
		}
	}
	values["FAAS_PROFILING_ENABLED"] = "1"
	values["FAAS_PROFILING_ENDPOINT"] = api.ProfileLocalEndpoint
	if exists(nodePath) {
		values["NODE_OPTIONS"] = strings.TrimSpace(values["NODE_OPTIONS"] + " --require=" + nodePath)
	}
	if exists(pythonPath) {
		values["PYTHONPATH"] = pythonPath + ":" + values["PYTHONPATH"]
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out
}

func (b *profileBridge) pushHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		http.Error(w, "profile bridge busy", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.ProfileMaxCompressedBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "profile push exceeds bounds", http.StatusBadRequest)
		return
	}
	uploads, err := profileproto.DecodePush(raw, r.Header.Get("Content-Encoding") == "gzip")
	if err != nil {
		http.Error(w, "invalid profile push", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	epoch := b.control.Epoch
	b.mu.Unlock()
	for _, item := range uploads {
		if item.Epoch != epoch || epoch == "" {
			http.Error(w, "profile belongs to an earlier collection epoch", http.StatusConflict)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProfileTransportTimeout)
	defer cancel()
	for _, item := range uploads {
		if err := b.accept(ctx, item.Epoch, item.Upload); err != nil {
			http.Error(w, "profile collection unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/proto")
	w.WriteHeader(http.StatusOK)
}
