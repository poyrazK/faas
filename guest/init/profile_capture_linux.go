//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

// captureInterruptedOOM explains a window ended by an OOM kill.
const captureInterruptedOOM = "the workload was OOM-killed during the capture; heap profiles are the last checkpoints before the kill"

// captureWait waits d unless stop closes first and reports whether it did.
// Tests inject sleep; production passes nil for an interruptible timer.
func captureWait(sleep func(time.Duration), d time.Duration, stop <-chan struct{}) bool {
	if sleep != nil {
		sleep(d)
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return false
	case <-stop:
		return true
	}
}

// runCapture arms the collectors for one on-demand window (ADR-1155). The
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
	c := &profileCapture{request: req, epoch: newProfileEpoch(), processes: map[string]bool{},
		checkpoints: map[string]profileproto.CapturedProfile{}, stop: make(chan struct{}), replied: make(chan struct{})}
	window := int((req.Duration() + time.Second - 1) / time.Second)
	b.capture = c
	b.control = profileControl{Enabled: true, Epoch: c.epoch, WindowSeconds: window, Kinds: append([]string(nil), req.Kinds...), Capture: true,
		CheckpointSeconds: int(req.Checkpoint() / time.Second)}
	b.mu.Unlock()

	interrupted := captureWait(sleep, req.Duration(), c.stop)
	b.mu.Lock()
	if b.capture == c && !b.control.Suspended {
		b.control.Enabled = false
	}
	b.mu.Unlock()
	grace := profileproto.CaptureGrace
	if interrupted {
		grace = api.ProfileCaptureInterruptGrace
	}
	captureWait(sleep, grace, nil)

	b.mu.Lock()
	defer b.mu.Unlock()
	result := profileproto.CaptureResult{Profiles: c.profiles, Processes: len(c.processes), Dropped: c.dropped, Reason: c.aborted}
	if result.Profiles == nil {
		result.Profiles = []profileproto.CapturedProfile{}
	}
	usedCheckpoint := c.addCheckpoints(&result)
	result.OOMKilled, result.OOMPeakMB = c.oomKilled, c.oomPeakMB
	if interrupted || usedCheckpoint {
		result.Partial = true
		if result.Reason == "" && interrupted {
			result.Reason = c.stopReason
		} else if result.Reason == "" {
			result.Reason = "a process stopped before the capture ended; its heap profile is the last checkpoint"
		}
	}
	b.finished = c
	if b.capture == c {
		b.capture = nil
		// A checkpoint during the grace period keeps the bridge suspended;
		// resume restores the base configuration.
		if !b.control.Suspended {
			b.control = b.base
			b.control.Epoch = newProfileEpoch()
		}
	}
	if result.Reason == "" {
		result.Reason = c.scrapeNote
	}
	if result.Reason == "" && result.Processes == 0 {
		result.Reason = c.probeNote
	}
	if result.Reason == "" && result.Processes == 0 {
		result.Reason = "no instrumented process polled the profiler during the capture"
	}
	return result, profileproto.CaptureAckOK
}

// addCheckpoints adds the last checkpoint of every process that delivered no
// final heap profile, within the reply bounds, and reports whether any was.
func (c *profileCapture) addCheckpoints(result *profileproto.CaptureResult) bool {
	final := map[string]bool{}
	for _, p := range result.Profiles {
		final[p.Kind+"/"+p.ProcessID] = true
	}
	keys := make([]string, 0, len(c.checkpoints))
	for key := range c.checkpoints {
		if !final[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	used := false
	for _, key := range keys {
		p := c.checkpoints[key]
		if len(result.Profiles) >= profileproto.CaptureMaxProfiles || c.bytes+len(p.Profile) > profileproto.CaptureMaxProfileBytes {
			result.Dropped++
			continue
		}
		result.Profiles = append(result.Profiles, p)
		c.bytes += len(p.Profile)
		used = true
	}
	return used
}

// interruptCaptureForOOM ends a running window because the workload was
// OOM-killed, marks its reply with the kill, and returns a channel that
// closes once the reply has reached the host (or at once when idle).
func (b *profileBridge) interruptCaptureForOOM(peakMB int) <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.capture; c != nil && c.stop != nil {
		if c.stopReason == "" {
			c.stopReason = captureInterruptedOOM
			c.oomKilled, c.oomPeakMB = true, peakMB
			close(c.stop)
		}
		return c.replied
	}
	if c := b.finished; c != nil && c.replied != nil {
		return c.replied
	}
	done := make(chan struct{})
	close(done)
	return done
}

// markReplied releases waiters on the capture whose reply was just written.
func (b *profileBridge) markReplied() {
	b.mu.Lock()
	c := b.finished
	b.finished = nil
	b.mu.Unlock()
	c.markReplied()
}

// flushProfileCaptureOnOOM ends a capture interrupted by an OOM kill and
// waits, bounded, until its checkpoints reached the host. guest-init calls
// it before reporting the kill: the host destroys the instance on that
// report, which would otherwise discard the heap captured before it.
func flushProfileCaptureOnOOM(timeout time.Duration, peakMB int) {
	b := activeProfileBridge()
	if b == nil {
		return
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-b.interruptCaptureForOOM(peakMB):
	case <-t.C:
	}
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
	bridge := activeProfileBridge()
	reply := profileCaptureReply(f, lengthHeader, bridge, nil)
	if _, err := f.Write(reply); err != nil {
		log.Warn("profile capture reply", "err", err)
	}
	if bridge != nil {
		bridge.markReplied()
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
