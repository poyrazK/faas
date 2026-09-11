//go:build linux

// Characterization probe (ADR-050 §3, ADR-051). On the FIRST cold
// boot of a new deployment, guest-init runs in characterizing mode:
// it observes what the customer app binds, runs L7 probes from
// inside the guest to disambiguate http / graphql / grpc, captures
// the supervisor's exit code, and ships one report over AF_VSOCK
// STREAM (port 1026, msgtype 3) to the host (CID 2 / VMADDR_CID_HOST)
// with a 1-byte ack — not DGRAM, because the report gates a deploy
// and a silent drop is not acceptable (ADR-051 §"Rejected
// alternatives"; ADR-047's DGRAM was a deliberate flip).
//
// Wire direction: GUEST INITIATES — guest dial(host CID 2, port
// 1026), sends the framed JSON, awaits 1-byte ack, retries with
// backoff (100/250/500 ms) bounded by the 10s characterization
// deadline. This is the inverse of the resume listener (1024) and
// the stateless advisory (1025 DGRAM); ports 1024/1025/1026 stay
// distinct so a host-side prefix collision is impossible.
//
// Lifecycle: caller (boot()) calls runCharacterization after the supervisor
// starts running the app. The probe reports as soon as a listener is observed,
// when the supervisor reaches a terminal outcome, or when the observation
// window expires. A missing signal falls back to the host readiness probe; a
// reported non-zero startup exit is a deploy failure with the captured log.

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
)

// Wire constants. Mirror pkg/fcvm/vmm.go in PR-D.
const (
	// VsockCharacterizationPort is the AF_VSOCK port the host accepts
	// on (guest-init dials host CID 2, port 1026). Must match
	// pkg/fcvm/vmm.go::VsockCharacterizationHostPort on the host.
	VsockCharacterizationPort uint32 = 1026
	// VsockCharacterizationMsgType is the wire-format discriminator.
	// Matches pkg/fcvm/vmm.go::VsockCharacterizationMsgResumeRdy. The
	// host's accept loop filters by msg_type prefix.
	VsockCharacterizationMsgType uint32 = 3
	// VsockCharacterizationMaxBody caps the JSON body at 128 KiB.
	// The typical report is <2 KiB; 128 KiB accommodates a long
	// log_tail (the customer-facing deploy-row surface) plus
	// listening_addrs for a polyglot app, plus the captured OpenAPI
	// doc body (issue #975 item #1 / ADR-122 — most real-world docs
	// are 8-30 KiB, occasional large apps reach 60-100 KiB). Must
	// mirror pkg/fcvm/vmm.go::VsockCharacterizationMaxBody — drift
	// triggers guest/init/characterize_linux_test.go::TestWireConstants_MatchHost.
	VsockCharacterizationMaxBody = 128 * 1024
	// VsockCharacterizationRetries is the number of attempts to ship
	// the report — first attempt + 3 retries with the backoff below.
	VsockCharacterizationRetries = 3
	// VsockCharacterizationAckTimeout is the per-attempt wait for the
	// 1-byte ack. Distinct from the overall characterization deadline
	// which is the boot-orchestration budget.
	VsockCharacterizationAckTimeout = 1500 * time.Millisecond
)

var vsockCharacterizationBackoff = []time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
}

// Wire-stable class hint literals (ADR-051 §"Consequences").
// The host re-derives the authoritative class from the observed
// signals; these are the guest's best-guess sentinels. Centralised
// here so a future class addition doesn't drift across the three
// return sites in runL7Probes + probeHTTP. Mirrors the string
// literals in pkg/api.CharacterizationReport.ObservedClass (the
// wire doc) — keep both in sync.
const (
	classHTTP    = "http"
	classJob     = "job"
	classWorker  = "worker"
	classGraphQL = "graphql"
	classGRPC    = "grpc"
)

// CharacterizationResult is what runCharacterization returns to the
// caller. The caller (boot()) uses it ONLY to decide whether to log
// a degradation (the platform never fails a deploy on a probe
// failure — the host falls back to the scan-hint class).
type CharacterizationResult struct {
	Mode          PortNormMode // "none" | "dnat" | "forward" — what rung
	Port          int          // the observed bind port (0 if none)
	ExitCode      int          // 0=clean, -1=still running, 1..255=failure
	ObservedClass string       // guest best-guess (host re-derives)
	Duration      time.Duration
	Shipped       bool   // true if the report landed with an ack
	Reason        string // "ok" | "ack_timeout" | "bind_timeout" | "send_error"
}

// RunArgs is the input bundle for the characterization probe. Split
// out so the test (characterize_linux_test.go) can drive a synthetic
// scenario without spinning up a real customer app.
type RunArgs struct {
	Manifest       api.AppManifest    // for the healthz / port-8080 baseline
	AppPID         func() int         // the supervisor's child PID; -1 = no child
	ExitStatus     func() (int, bool) // latest exit code + whether an exit happened
	RingBufferTail func() string      // the supervisor's log ring buffer tail
	Log            *slog.Logger
	Now            func() time.Time // injectable for tests
}

// runCharacterization is the boot-side entry point. It observes the
// app's first 10 s of life (or until exit), runs L7 probes against
// the observed bind, and ships the report. Tolerates every failure
// path — the platform's contract is "no signal" not "won't boot".
func runCharacterization(ctx context.Context, args RunArgs) CharacterizationResult {
	if args.Log == nil {
		args.Log = slog.Default()
	}
	if args.Now == nil {
		args.Now = time.Now
	}

	start := args.Now()
	res := CharacterizationResult{Reason: "ok", ExitCode: -1}
	defer func() {
		res.Duration = args.Now().Sub(start)
		args.Log.Info("characterization complete",
			"mode", res.Mode, "port", res.Port, "class", res.ObservedClass,
			"exit", res.ExitCode, "shipped", res.Shipped, "reason", res.Reason,
			"duration", res.Duration)
	}()

	// 1. Observe the bind: watch /proc/net/tcp{,6} filtered to the
	// supervisor's child PID's socket inodes. First LISTEN entry
	// wins; cap the wait at the characterization deadline.
	obs, observedAddr, exitCode, exited := waitForBind(ctx, args, &res)
	if exited {
		res.ExitCode = exitCode
	}
	if !obs {
		res.Reason = "bind_timeout"
	}

	// 2. Run L7 probes against the observed port. Each probe is a
	// goroutine; we record the first positive outcome and move on.
	// If no bind, every probe is a fast-fail; exit status distinguishes a
	// completed job, a running worker, and a startup failure.
	//
	// ADR-122 §D4: probeHTTP now always runs and may capture an
	// OpenAPI doc. The tuple carries (class, openapi_doc, truncated).
	// The doc fields survive the round-trip regardless of which
	// probe won the class hint.
	classHint, openAPIDoc, openAPIDocTruncated := runL7Probes(ctx, args, res.Port)
	res.ObservedClass = deriveGuestCharacterizationClass(res.Port, classHint, res.ExitCode, exited)

	// 3. Pick a portnorm mode. Only a positive observed port is a
	// meaningful normalization target. A bind timeout is a valid
	// characterization result (jobs and crash-looping apps commonly
	// have no listener); trying to install DNAT to port 0 would invoke
	// iptables with an invalid rule and then startForwarder would fail.
	if res.Port > 0 {
		// manifest.Port==0 means DefaultAppPort was effective (the
		// customer's process is expected to bind 8080). Anything else
		// requires a ladder rung.
		res.Mode = choosePortNormMode(args.Manifest, res.Port)
		if res.Mode == PortNormDNAT {
			if err := installDNAT(res.Port, args.Log); err != nil {
				res.Mode = PortNormForward
				_, _ = startForwarder(res.Port, args.Log)
			}
		}
		if res.Mode == PortNormForward {
			ln, fErr := startForwarder(res.Port, args.Log)
			if fErr != nil || ln == nil {
				args.Log.Warn("portnorm forward failed; host will see listen mismatch",
					"port", res.Port, "err", fErr)
			}
		}
	} else {
		res.Mode = PortNormNone
		args.Log.Debug("portnorm skipped: no observed listener", "port", res.Port)
	}

	// 4. Build the report and ship it. A bound server is reported
	// immediately with exit_code=-1; waiting for a long-running server to exit
	// would deadlock the readiness handshake. No-bind workloads consume the
	// observation window (or stop early on exit) to distinguish job, worker,
	// and startup failure.
	addr := observedAddr
	if addr == "" {
		addr = fmt.Sprintf("127.0.0.1:%d", res.Port)
	}
	r := api.CharacterizationReport{
		ObservedClass:         res.ObservedClass,
		ObservedPort:          res.Port,
		ExitCode:              res.ExitCode,
		ListeningAddrs:        []string{addr},
		OutboundCount:         countOutboundLinux(args.AppPID()),
		LogTail:               truncateLog(args.RingBufferTail(), VsockCharacterizationMaxBody),
		PortNormalizationMode: string(res.Mode),
		// ADR-122 §D2/D4 — endpoint discovery. ProbeHTTP captures
		// the OpenAPI doc (if any) and ships it on the wire. The
		// truncation flag is the customer's signal that the doc
		// exceeded the 128 KiB cap and was hard-truncated at the
		// guest (the apid PATCH endpoint is the recovery path).
		OpenAPIDoc:          openAPIDoc,
		OpenAPIDocTruncated: openAPIDocTruncated,
	}
	if !shipReport(r, args.Log) {
		res.Shipped = false
		if res.Reason == "ok" {
			res.Reason = "ack_timeout"
		}
	} else {
		res.Shipped = true
	}
	return res
}

func deriveGuestCharacterizationClass(port int, l7Hint string, exitCode int, exited bool) string {
	if port > 0 {
		if l7Hint == "" {
			return classHTTP
		}
		return l7Hint
	}
	if !exited {
		return classWorker
	}
	if exitCode == 0 {
		return classJob
	}
	return ""
}

// waitForBind polls /proc/net/tcp{,6} for a LISTEN socket owned by
// the app's PID tree. Returns the first match and the address
// string for the report. On timeout returns observed=false and
// observedAddr="" — the host derives job, worker, or startup failure from the
// terminal status. The
// deadline comes from api.CharacterizationDeadline (ADR-051
// §"Characterization window"), the single source shared with the
// host's wait in pkg/fcvm/manager.go.
func waitForBind(ctx context.Context, args RunArgs, res *CharacterizationResult) (bool, string, int, bool) {
	deadline := args.Now().Add(api.CharacterizationDeadline)
	for {
		if port, addr, ok := probeListening(args.AppPID()); ok {
			res.Port = port
			return true, addr, 0, false
		}
		if args.ExitStatus != nil {
			if code, exited := args.ExitStatus(); exited {
				return false, "", code, true
			}
		}
		if args.Now().After(deadline) {
			return false, "", 0, false
		}
		select {
		case <-ctx.Done():
			return false, "", 0, false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// probeListeningLinux is the linux-only /proc walk. Called by
// probeListening (characterize_common.go) on linux after the
// no-child early-out passes. Cross-platform callers always go
// through probeListening; the unit test pins the early-out
// contract there.
func probeListeningLinux(pid int) (int, string, bool) {
	ownedInodes := ownedSocketInodes(pid)
	if len(ownedInodes) == 0 {
		return 0, "", false
	}
	if port, addr, ok := scanListeningFile("/proc/net/tcp", ownedInodes); ok {
		return port, addr, true
	}
	if port, addr, ok := scanListeningFile("/proc/net/tcp6", ownedInodes); ok {
		return port, addr, true
	}
	return 0, "", false
}

// countOutboundLinux returns the number of ESTABLISHED TCP
// connections owned by the app's process tree at this moment.
// This is the worker-signal the host uses (per ADR-051
// §"Consequences"): job = 0 outbound + exit 0; worker = ≥1
// outbound + still running. The query is a snapshot — we read
// /proc/net/tcp + /proc/net/tcp6 once and return. Race-free: the
// kernel owns these counters under the socket lock; our read is a
// single open + scan, no incremental update between /proc reads.
//
// Walks the entire app process tree via ownedSocketInodes (which
// recurses through /proc/<pid>/task/<tid>/children), so Node
// cluster-mode workers and setpgid-rebased children count toward
// the worker's outbound signal. Returns 0 on any failure path (a
// missing /proc, a parse error, an empty inode set) — a missing
// count is a legitimate "no outbound" signal, not a boot-fatal
// error.
func countOutboundLinux(pid int) int {
	if pid <= 0 {
		return 0
	}
	ownedInodes := ownedSocketInodes(pid)
	if len(ownedInodes) == 0 {
		return 0
	}
	return scanEstablishedFile("/proc/net/tcp", ownedInodes) +
		scanEstablishedFile("/proc/net/tcp6", ownedInodes)
}

// ownedSocketInodesRecursiveDepth bounds the recursive process-tree
// walk in ownedSocketInodes. 8 covers a Node cluster master + a few
// worker generations, a Go app that uses setpgid + a couple of
// re-execs, and any realistic customer shape. A larger cap invites a
// runaway walk on a pathological forker; a smaller cap would miss
// legitimate fork chains. Pinned by TestOwnedSocketInodes_DepthBounded.
const ownedSocketInodesRecursiveDepth = 8

// ownedSocketInodes walks /proc/<pid>/fd/* for the pid AND for every
// child reachable via /proc/<pid>/task/<tid>/children, looking for
// the symlink target of the form `socket:[<inode>]`. Returns the
// union of socket inodes the process tree owns. Returns nil if
// /proc isn't mounted (e.g. /proc unmounted post-pivot — defensive,
// shouldn't happen here).
//
// The recursive walk is the load-bearing fix for ADR-051
// §"Common failures": a Node cluster-mode app (master forks
// workers, each binds :8080), a Go app that uses setpgid, or any
// customer that forks long-lived children, has its worker sockets
// invisible to an immediate-PID-only walk. Bounded by
// ownedSocketInodesRecursiveDepth and a visited-set so a kernel-
// level cycle in /proc/<pid>/task/<tid>/children cannot loop.
func ownedSocketInodes(pid int) map[uint64]struct{} {
	out := make(map[uint64]struct{})
	visited := make(map[int]bool)
	collectSocketInodes(pid, 0, out, visited)
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectSocketInodes walks one PID's /proc/<pid>/fd, then recurses
// into every child PID listed under /proc/<pid>/task/<tid>/children,
// up to ownedSocketInodesRecursiveDepth. visited guards against
// cycles (defensive — PIDs are unique per process so a true cycle
// is impossible without CLONE_NEWPID, which the guest doesn't
// allow). Returns silently on any read failure per the tolerated-
// failure pattern (resume hook line 70, stateless advisory line 79).
func collectSocketInodes(pid int, depth int, out map[uint64]struct{}, visited map[int]bool) {
	if pid <= 0 || depth > ownedSocketInodesRecursiveDepth || visited[pid] {
		return
	}
	visited[pid] = true

	// 1. Collect this PID's socket inodes.
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	//nolint:forbidigo // /proc/<pid>/fd is a vetted kernel path inside the
	// guest; the customer-path guard (openCustomerFile) is for host daemons
	// reading customer bytes — this reads in-guest kernel state only.
	f, err := os.Open(dir)
	if err == nil {
		for {
			names, rErr := f.Readdirnames(64)
			for _, n := range names {
				link, lErr := os.Readlink(dir + "/" + n)
				if lErr != nil {
					continue
				}
				// Format: "socket:[12345]"
				if !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
					continue
				}
				var inode uint64
				if _, sErr := fmt.Sscanf(link[len("socket:["):len(link)-1], "%d", &inode); sErr == nil {
					out[inode] = struct{}{}
				}
			}
			if rErr == io.EOF {
				break
			}
			if rErr != nil {
				break
			}
		}
		_ = f.Close()
	}

	// 2. Recurse into children. /proc/<pid>/task/<tid>/children
	// is one whitespace-separated PID list per task. A thread with
	// no children has an empty file (read returns 0 bytes, no error).
	// We read every task's children list — a forked process inherits
	// one task from the parent; multi-threaded apps that fork expose
	// the child via any one of the parent's tasks.
	taskDir := fmt.Sprintf("/proc/%d/task", pid)
	//nolint:forbidigo // /proc/<pid>/task is a vetted kernel path inside the guest; the customer-path guard (openCustomerFile) is for host daemons reading customer bytes — this reads in-guest kernel state only.
	tf, tErr := os.Open(taskDir)
	if tErr != nil {
		return
	}
	tNames, _ := tf.Readdirnames(64)
	_ = tf.Close()
	for _, tid := range tNames {
		childrenPath := fmt.Sprintf("%s/%s/children", taskDir, tid)
		data, cErr := os.ReadFile(childrenPath)
		if cErr != nil || len(data) == 0 {
			continue
		}
		for _, field := range strings.Fields(string(data)) {
			var child int
			if _, pErr := fmt.Sscanf(field, "%d", &child); pErr != nil {
				continue
			}
			collectSocketInodes(child, depth+1, out, visited)
		}
	}
}

// runL7Probes kicks off the three probes concurrently against the
// observed port. Returns the first non-empty class hint and falls
// back to "http" if every probe is inconclusive. The 2 s ctx budget
// is bounded — a slow / unresponsive listener costs us 2 s of boot,
// not unbounded hangs.
//
// ADR-122 §D4: probeHTTP now ALWAYS runs (previously it only ran
// when probeGraphQL + probeGRPC were both empty). The body capture
// is the load-bearing endpoint discovery path; the class hint logic
// is unchanged (first non-empty wins).
func runL7Probes(ctx context.Context, _ RunArgs, port int) (string, []byte, bool) {
	if port <= 0 {
		// No bind means there is no L7 class hint. The caller combines the
		// terminal status with this observation; the host re-derives it again.
		return "", nil, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	probes := []func() probeResult{
		probeGraphQL(port),
		probeGRPC(port),
		probeHTTP(port),
	}
	resCh := make(chan probeResult, len(probes))
	for _, fn := range probes {
		fn := fn
		go func() {
			resCh <- fn()
		}()
	}
	collected := 0
	var openAPIDoc []byte
	var openAPIDocTruncated bool
	for collected < len(probes) {
		select {
		case <-probeCtx.Done():
			// Deadline: return whatever openapi_doc we got, no
			// class hint (the engine re-derives from runtime shape).
			return "", openAPIDoc, openAPIDocTruncated
		case r := <-resCh:
			collected++
			if r.Class != "" {
				// First non-empty class wins; the openapi_doc fields
				// from the probeHTTP result (if any) survive the
				// round-trip regardless of which probe won.
				if r.OpenAPIDoc != nil {
					openAPIDoc = r.OpenAPIDoc
					openAPIDocTruncated = r.OpenAPIDocTruncated
				}
				return r.Class, openAPIDoc, openAPIDocTruncated
			}
			// Empty class — keep collecting, but capture the
			// openapi_doc fields from probeHTTP even though the
			// class is empty (a non-OpenAPI HTTP service still
			// runs probeHTTP and may capture a doc).
			if r.OpenAPIDoc != nil && openAPIDoc == nil {
				openAPIDoc = r.OpenAPIDoc
				openAPIDocTruncated = r.OpenAPIDocTruncated
			}
		}
	}
	// All probes ran without a class hint. Surface the captured
	// openapi_doc (if any) but DO NOT default to classHTTP — the
	// engine re-derives from runtime shape (workload class hint is
	// a guest best-guess, not authoritative). ADR-051 §"Class hint
	// fallback": "no probe matched" is itself a signal.
	return "", openAPIDoc, openAPIDocTruncated
}

// probeResult is the inner return shape of every probe.
// Class is the workload-class hint (empty = inconclusive).
// OpenAPIDoc + OpenAPIDocTruncated are populated by probeHTTP
// only; the other probes leave them at the zero value.
type probeResult struct {
	Class               string
	OpenAPIDoc          []byte
	OpenAPIDocTruncated bool
}

func probeGraphQL(port int) func() probeResult {
	return func() probeResult {
		c := &http.Client{Timeout: 1 * time.Second}
		// GraphQL `__schema` introspection — most servers reply 200
		// with a body containing "data.__schema". We use a small
		// POST with a valid query and check the body contains the
		// canonical field.
		//
		// ADR-122 §D4: GraphQL introspection capture is OUT of
		// scope for this PR — left as a discard. GraphQL schema
		// capture is issue #975 item #2 (separate ADR).
		body := strings.NewReader(`{"query":"{__schema{queryType{name}}}"}`)
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/", port), body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return probeResult{}
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return probeResult{}
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if bytes.Contains(raw, []byte("__schema")) {
			return probeResult{Class: classGraphQL}
		}
		return probeResult{}
	}
}

func probeGRPC(port int) func() probeResult {
	return func() probeResult {
		// gRPC servers respond to a GET with the HTTP/2 SETTINGS
		// frame and `:status 200`; a generic GET to the port is
		// mostly going to fail. A more honest check is to dial,
		// send an empty HTTP/2 preface, and observe `:status` 200
		// in the response. For the characterize probe we keep
		// this brief: TCP-level open + check for the canonical
		// gRPC response header "content-type: application/grpc".
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1*time.Second)
		if err != nil {
			return probeResult{}
		}
		defer func() { _ = c.Close() }()
		// gRPC reflection protocol — send the bare minimum probe
		// (raw bytes that look like HTTP/2 magic).
		_, _ = c.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
		buf := make([]byte, 256)
		_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _ := c.Read(buf)
		if n > 0 && bytes.Contains(buf[:n], []byte("content-type")) {
			// Probably gRPC reflection; the engine re-derives.
			return probeResult{Class: classGRPC}
		}
		return probeResult{}
	}
}

func probeHTTP(port int) func() probeResult {
	return func() probeResult {
		// GET /openapi.json + body capture. ADR-122 §D4: the body
		// is the load-bearing endpoint discovery payload. The probe:
		//
		//  1. Reads the body up to VsockCharacterizationMaxBody
		//     (128 KiB after the wire-format bump).
		//  2. Validates Content-Type is application/json or
		//     application/openapi+json.
		//  3. Cheap shape sniff: the body must contain a top-level
		//     `openapi` (3.0/3.1) or `swagger` key — prevents a
		//     serving SPA from being mis-classified as an OpenAPI
		//     doc. The strict Draft-2020-12 validation lives at
		//     the apid PATCH path, not here.
		//  4. Sets the OpenAPIDoc + OpenAPIDocTruncated fields on
		//     the result. The truncation flag is set when the
		//     read buffer length is exactly 128 KiB AND the
		//     connection didn't close cleanly (EOF) — i.e. the
		//     doc was larger than what we captured.
		c := &http.Client{Timeout: 1 * time.Second}
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/openapi.json", port), nil)
		if err != nil {
			return probeResult{}
		}
		req.Header.Set("Host", "127.0.0.1")
		req.Header.Set("Connection", "close")
		resp, err := c.Do(req)
		if err != nil {
			return probeResult{}
		}
		defer func() { _ = resp.Body.Close() }()
		// 200-class status is the only thing that counts as an
		// http-class hit. 3xx (redirect) falls through to the
		// class == "" branch; 4xx/5xx likewise.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return probeResult{}
		}
		// Content-Type gate. We accept application/json or
		// application/openapi+json (the canonical OpenAPI 3.1
		// type). Anything else is dropped silently.
		ctype := resp.Header.Get("Content-Type")
		if !isOpenAPIContentType(ctype) {
			// Still an http-class hit (we got a 200 OK), but no
			// doc to capture.
			return probeResult{Class: classHTTP}
		}
		// Read the body up to the cap. We read up to N+1 bytes
		// (LimitReader caps at N) so we can detect overflow: if
		// we read N+1 bytes, the doc was larger than what we
		// captured and the truncation flag MUST be set.
		const cap = VsockCharacterizationMaxBody
		body, err := io.ReadAll(io.LimitReader(resp.Body, int64(cap)+1))
		if err != nil {
			return probeResult{Class: classHTTP}
		}
		truncated := false
		if len(body) > cap {
			truncated = true
			body = body[:cap]
		}
		// Shape sniff: top-level `openapi` or `swagger` key.
		if !looksLikeOpenAPIDoc(body) {
			return probeResult{Class: classHTTP}
		}
		return probeResult{
			Class:               classHTTP,
			OpenAPIDoc:          body,
			OpenAPIDocTruncated: truncated,
		}
	}
}

// isOpenAPIContentType accepts the canonical OpenAPI + JSON
// content types. Anything else is dropped silently — a
// text/html response is treated as "no OpenAPI doc served".
func isOpenAPIContentType(ct string) bool {
	ct = strings.TrimSpace(strings.ToLower(ct))
	return strings.HasPrefix(ct, "application/json") ||
		strings.HasPrefix(ct, "application/openapi+json")
}

// looksLikeOpenAPIDoc is the cheap shape sniff. The body must
// contain a top-level `openapi` ("3.0"/"3.1") or `swagger` key —
// NOT a key nested inside a different root object
// (`{"data":{"openapi":...}}` is rejected).
//
// We do a linear scan of the first 4 KiB rather than a full JSON
// parse — the cap is 128 KiB and the keys are at the head of the
// doc. Brace depth tracking separates top-level keys from inner
// ones: a "openapi"/"swagger" string is matched only at depth == 1
// (inside the root object but not inside a nested `{...}`).
func looksLikeOpenAPIDoc(body []byte) bool {
	// Trim leading whitespace.
	start := 0
	for start < len(body) {
		c := body[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	if start >= len(body) || body[start] != '{' {
		return false
	}
	// Scan the first 4 KiB at depth-tracked. depth==1 means inside
	// the root object; depth>=2 means inside a nested object.
	head := body[start:]
	if len(head) > 4096 {
		head = head[:4096]
	}
	depth := 0
	for j := 0; j < len(head); j++ {
		switch head[j] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				// End of root object reached. The rest is
				// trailing whitespace — nothing more to scan.
				return false
			}
		case '"':
			// String literal at depth==1 is a top-level key.
			// Match "openapi" or "swagger" exactly (the JSON
			// spec requires ASCII letters after the quote).
			if depth == 1 {
				rest := head[j:]
				if bytes.HasPrefix(rest, []byte(`"openapi"`)) ||
					bytes.HasPrefix(rest, []byte(`"swagger"`)) {
					return true
				}
			}
		}
	}
	return false
}

// shipReport dials host CID 2 (VMADDR_CID_HOST) at the char port,
// writes [msg_type 4 BE][body_len 4 BE][body], and awaits a 1-byte
// ack with the 1.5s per-attempt timeout. Returns true on ack OK.
func shipReport(r api.CharacterizationReport, log *slog.Logger) bool {
	body, err := marshalCharacterizationReport(r)
	if err != nil {
		log.Warn("characterization marshal failed", "err", err)
		return false
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], VsockCharacterizationMsgType)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	payload := append(hdr[:], body...)

	attempts := VsockCharacterizationRetries + 1
	for i := 0; i < attempts; i++ {
		if ok := shipOnce(payload); ok {
			return true
		}
		if i < attempts-1 {
			// There are three configured backoff intervals for the
			// first three attempts. The final retry has no following
			// sleep; keep this bounds check explicit so a retry-count
			// change cannot turn a missing host-vsock endpoint into a
			// guest-init panic.
			if i < len(vsockCharacterizationBackoff) {
				time.Sleep(vsockCharacterizationBackoff[i])
			}
		}
	}
	return false
}

// marshalCharacterizationReport keeps the JSON frame structurally valid while
// fitting it inside the wire cap. LogTail keeps its newest data; OpenAPIDoc
// keeps its prefix and advertises truncation. Raw truncation after json.Marshal
// is forbidden because it produces a frame the host can never decode.
func marshalCharacterizationReport(r api.CharacterizationReport) ([]byte, error) {
	r.LogTail = strings.ToValidUTF8(truncateLog(r.LogTail, api.LogRingBufferBytes), "\uFFFD")
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(body) <= VsockCharacterizationMaxBody {
		return body, nil
	}

	openAPIDoc := r.OpenAPIDoc
	r.OpenAPIDoc = nil
	if len(openAPIDoc) > 0 {
		r.OpenAPIDocTruncated = true
	}
	body, err = json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(body) > VsockCharacterizationMaxBody && r.LogTail != "" {
		logRunes := []rune(r.LogTail)
		low, high := 0, len(logRunes)
		for low < high {
			mid := low + (high-low+1)/2
			r.LogTail = string(logRunes[len(logRunes)-mid:])
			candidate, marshalErr := json.Marshal(r)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if len(candidate) <= VsockCharacterizationMaxBody {
				low = mid
			} else {
				high = mid - 1
			}
		}
		r.LogTail = string(logRunes[len(logRunes)-low:])
		body, err = json.Marshal(r)
		if err != nil {
			return nil, err
		}
	}
	if len(body) > VsockCharacterizationMaxBody {
		return nil, fmt.Errorf("characterization metadata is %d bytes (max %d)", len(body), VsockCharacterizationMaxBody)
	}
	if len(openAPIDoc) == 0 {
		return body, nil
	}

	low, high := 0, len(openAPIDoc)
	for low < high {
		mid := low + (high-low+1)/2
		r.OpenAPIDoc = openAPIDoc[:mid]
		candidate, marshalErr := json.Marshal(r)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if len(candidate) <= VsockCharacterizationMaxBody {
			low = mid
		} else {
			high = mid - 1
		}
	}
	r.OpenAPIDoc = openAPIDoc[:low]
	body, err = json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(body) > VsockCharacterizationMaxBody {
		return nil, fmt.Errorf("characterization body is %d bytes (max %d)", len(body), VsockCharacterizationMaxBody)
	}
	return body, nil
}

// shipOnce is a single attempt: open STREAM, send, read 1 byte.
func shipOnce(payload []byte) bool {
	sock, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer func() { _ = unix.Close(sock) }()
	// Set per-socket deadlines via SO_SNDTIMEO / SO_RCVTIMEO
	// (golang.org/x/sys/unix has no SetDeadline helper for AF_VSOCK
	// sockets; the kernel-level setsockopt works on all sockets). Install them
	// before Connect so an unavailable host endpoint cannot block forever.
	if err := setSockTimeout(sock, unix.SO_SNDTIMEO, VsockCharacterizationAckTimeout); err != nil {
		return false
	}
	if err := setSockTimeout(sock, unix.SO_RCVTIMEO, VsockCharacterizationAckTimeout); err != nil {
		return false
	}
	addr := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_HOST,
		Port: VsockCharacterizationPort,
	}
	if err := unix.Connect(sock, addr); err != nil {
		return false
	}
	if !writeFullSocket(sock, payload) {
		return false
	}
	var ack [1]byte
	for {
		n, readErr := unix.Read(sock, ack[:])
		if readErr == unix.EINTR {
			continue
		}
		return readErr == nil && n == len(ack) && ack[0] == 0
	}
}

func writeFullSocket(fd int, payload []byte) bool {
	for len(payload) > 0 {
		n, err := unix.Write(fd, payload)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return false
		}
		payload = payload[n:]
	}
	return true
}

// setSockTimeout applies a SO_*_TIMEO setsockopt on a raw socket fd.
// Used to bound shipOnce's Connect/Read/Write because golang.org/x/sys/unix
// has no per-fd deadline helper for AF_VSOCK. A failed setsockopt is fatal to
// the attempt: continuing would turn a bounded readiness signal into an
// indefinitely blocked guest-init goroutine.
func setSockTimeout(fd int, opt int, d time.Duration) error {
	tv := unix.Timeval{
		Sec:  int64(d / time.Second),
		Usec: int64(d%time.Second) / 1000,
	}
	return unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, opt, &tv)
}

// truncateLog lives in characterize_common.go (build-tag-free).
