package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/crashcapturewire"
)

// crashCapturePath is the ADR-733 SDK trigger on the guest metadata
// endpoint: POST http://169.254.169.254/v1/crash-snapshots:capture from an
// app's error handler captures the app's own instance while the call blocks.
const crashCapturePath = "/v1/crash-snapshots:capture"

// crashCaptureForkGrace is how long a reset stream waits for the restore
// hook before guest-init decides it was not restored into a fork.
const crashCaptureForkGrace = 3 * time.Second

func crashCaptureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeCrashCaptureResponse(w, http.StatusMethodNotAllowed, crashcapturewire.Response{Status: "method_not_allowed"})
		return
	}
	var req crashcapturewire.Request
	body, err := io.ReadAll(io.LimitReader(r.Body, crashcapturewire.MaxFrame+1))
	if err != nil || len(body) > crashcapturewire.MaxFrame || (len(body) > 0 && json.Unmarshal(body, &req) != nil) {
		writeCrashCaptureResponse(w, http.StatusBadRequest, crashcapturewire.Response{Status: "invalid_request"})
		return
	}
	// The metadata server's write timeout is sized for quick calls; this
	// one blocks while the host captures the instance.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(req.Wait() + 15*time.Second))
	resp := requestCrashCapture(req, crashCaptureDial)
	writeCrashCaptureResponse(w, crashCaptureHTTPStatus(resp), resp)
}

// requestCrashCapture runs one request over a fresh host stream. A stream
// that breaks after the capture was requested, followed by a restore, means
// this code is now running in a fork of the capture.
func requestCrashCapture(req crashcapturewire.Request, dial func() (net.Conn, error)) crashcapturewire.Response {
	if dial == nil {
		return crashcapturewire.Response{Status: crashcapturewire.StatusUnavailable, Code: "host_unreachable"}
	}
	gen := restoreGeneration.current()
	conn, err := dial()
	if err != nil {
		return crashcapturewire.Response{Status: crashcapturewire.StatusUnavailable, Code: "host_unreachable"}
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(req.Wait() + 10*time.Second))
	if err := crashcapturewire.WriteFrame(conn, req); err != nil {
		return crashcapturewire.Response{Status: crashcapturewire.StatusUnavailable, Code: "host_unreachable"}
	}
	var last crashcapturewire.Response
	for {
		var resp crashcapturewire.Response
		if err := crashcapturewire.ReadFrame(conn, &resp); err != nil {
			// Any error counts: in a fork the stream is reset, and the
			// restore steps the clock past the stream's deadline.
			if last.CaptureID != "" && restoreGeneration.changedSince(gen, crashCaptureForkGrace) {
				return crashcapturewire.Response{Status: crashcapturewire.StatusCaptured, CaptureID: last.CaptureID, InFork: true}
			}
			if last.CaptureID != "" {
				return crashcapturewire.Response{Status: crashcapturewire.StatusPending, CaptureID: last.CaptureID}
			}
			return crashcapturewire.Response{Status: crashcapturewire.StatusUnavailable, Code: "host_unreachable"}
		}
		if resp.Final() {
			return resp
		}
		last = resp
	}
}

// crashCaptureDial opens the host stream; set on Linux, where AF_VSOCK exists.
var crashCaptureDial func() (net.Conn, error)

func crashCaptureHTTPStatus(resp crashcapturewire.Response) int {
	switch resp.Status {
	case crashcapturewire.StatusCaptured:
		return http.StatusOK
	case crashcapturewire.StatusPending:
		return http.StatusAccepted
	case crashcapturewire.StatusRefused:
		return http.StatusConflict
	case crashcapturewire.StatusFailed:
		return http.StatusBadGateway
	default:
		return http.StatusServiceUnavailable
	}
}

func writeCrashCaptureResponse(w http.ResponseWriter, status int, resp crashcapturewire.Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
