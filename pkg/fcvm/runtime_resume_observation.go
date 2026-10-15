package fcvm

// adr: 595 Native promotion must retain the commands actually acknowledged.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// These are transport facts only. They do not prove process ownership, current
// policy authority or guest readiness, and do not enable paused promotion.
type runtimeResumeCommandAcknowledgment struct {
	Version             uint32
	CommandHash         string
	CompletedAtUnixNano int64
}

type runtimeResumeHookAcknowledgment struct {
	Version             uint32
	PayloadHash         string
	HostTimeUnixNano    int64
	CompletedAtUnixNano int64
}

func runtimeResumePayloadHash(domain string, payload []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Keep the strict acknowledgment separate from ResumeVM's legacy 409 retry
// behavior. A conflict cannot prove that this command resumed a paused owner.
func (v *JailerVMM) resumeVMObserved(ctx context.Context, l Lease) (runtimeResumeCommandAcknowledgment, error) {
	if v == nil || l.Instance == "" {
		return runtimeResumeCommandAcknowledgment{}, fmt.Errorf("vmm: resume: invalid VMM or instance")
	}
	raw := json.RawMessage(`{"state":"Resumed"}`)
	if err := errors.Join(v.apiPatch(ctx, l.Instance, "/vm", raw), ctx.Err()); err != nil {
		return runtimeResumeCommandAcknowledgment{}, err
	}
	return runtimeResumeCommandAcknowledgment{Version: 1,
		CommandHash:         runtimeResumePayloadHash("gregale.runtime-resume.command.v1\x00", raw),
		CompletedAtUnixNano: time.Now().UnixNano()}, nil
}

// Only the complete frame that received ACK=0 supplies the witness. Lost ACKs
// retry with fresh entropy; unsuccessful attempts never leave partial evidence.
func (v *JailerVMM) triggerResumeHookObserved(ctx context.Context, l Lease, hostTimeUnixNano int64) (runtimeResumeHookAcknowledgment, error) {
	var observed runtimeResumeHookAcknowledgment
	err := retryResumeTransport(ctx, func(callCtx context.Context) error {
		observed = runtimeResumeHookAcknowledgment{}
		return v.triggerResumeHookOnce(callCtx, l, hostTimeUnixNano, &observed)
	})
	if err = errors.Join(err, ctx.Err()); err != nil {
		return runtimeResumeHookAcknowledgment{}, err
	}
	return observed, nil
}

func writeResumeHookFrame(w io.Writer, frame []byte) error {
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}
