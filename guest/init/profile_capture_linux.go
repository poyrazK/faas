//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/profileproto"
)

// runCapture arms the collectors for one on-demand window (ADR-967). The
// capture epoch makes collectors restart with the requested kinds; disabling
// it at the window end (same epoch) makes them stop and flush. The grace
// period then collects final uploads before the continuous configuration
// returns under a fresh epoch.
func (b *profileBridge) runCapture(req profileproto.CaptureRequest, sleep func(time.Duration)) (profileproto.CaptureResult, byte) {
	b.mu.Lock()
	if b.control.Suspended {
		b.mu.Unlock()
		return profileproto.CaptureResult{}, profileproto.CaptureAckSuspended
	}
	if b.capture != nil {
		b.mu.Unlock()
		return profileproto.CaptureResult{}, profileproto.CaptureAckBusy
	}
	c := &profileCapture{request: req, epoch: newProfileEpoch(), processes: map[string]bool{}}
	window := int((req.Duration() + time.Second - 1) / time.Second)
	b.capture = c
	b.control = profileControl{Enabled: true, Epoch: c.epoch, WindowSeconds: window, Kinds: append([]string(nil), req.Kinds...), Capture: true}
	b.mu.Unlock()

	sleep(req.Duration())
	b.mu.Lock()
	if b.capture == c && !b.control.Suspended {
		b.control.Enabled = false
	}
	b.mu.Unlock()
	sleep(profileproto.CaptureGrace)

	b.mu.Lock()
	defer b.mu.Unlock()
	result := profileproto.CaptureResult{Profiles: c.profiles, Processes: len(c.processes), Dropped: c.dropped, Reason: c.aborted}
	if result.Profiles == nil {
		result.Profiles = []profileproto.CapturedProfile{}
	}
	if b.capture == c {
		b.capture = nil
		// A checkpoint during the grace period keeps the bridge suspended;
		// resume restores the base configuration.
		if !b.control.Suspended {
			b.control = b.base
			b.control.Epoch = newProfileEpoch()
		}
	}
	if result.Reason == "" && result.Processes == 0 {
		result.Reason = "no instrumented process polled the profiler during the capture"
	}
	return result, profileproto.CaptureAckOK
}

func activeProfileBridge() *profileBridge {
	guestProfiles.Lock()
	defer guestProfiles.Unlock()
	return guestProfiles.bridge
}

// handleProfileCaptureConn serves a host-initiated capture on the control
// listener. The reply is an ack byte; OK is followed by a 4-byte big-endian
// length and the CaptureResult JSON. The host bounds and forwards the reply
// without parsing the profiles.
func handleProfileCaptureConn(f *os.File, log *slog.Logger, lengthHeader []byte) {
	reply := profileCaptureReply(f, lengthHeader, activeProfileBridge(), time.Sleep)
	if _, err := f.Write(reply); err != nil {
		log.Warn("profile capture reply", "err", err)
	}
}

func profileCaptureReply(r io.Reader, lengthHeader []byte, bridge *profileBridge, sleep func(time.Duration)) []byte {
	n := binary.BigEndian.Uint32(lengthHeader)
	if n == 0 || n > profileproto.CaptureMaxRequestBytes {
		return []byte{profileproto.CaptureAckInvalid}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return []byte{profileproto.CaptureAckInvalid}
	}
	var req profileproto.CaptureRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Validate() != nil {
		return []byte{profileproto.CaptureAckInvalid}
	}
	if bridge == nil {
		return []byte{profileproto.CaptureAckUnsupported}
	}
	result, ack := bridge.runCapture(req, sleep)
	if ack != profileproto.CaptureAckOK {
		return []byte{ack}
	}
	out, err := json.Marshal(result)
	if err != nil || len(out) > profileproto.CaptureMaxReplyBytes {
		return []byte{profileproto.CaptureAckInvalid}
	}
	reply := make([]byte, 5, 5+len(out))
	reply[0] = profileproto.CaptureAckOK
	binary.BigEndian.PutUint32(reply[1:5], uint32(len(out)))
	return append(reply, out...)
}
