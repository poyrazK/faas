//go:build linux

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/resumetimingproto"
	"golang.org/x/sys/unix"
)

const restoreReceiptTestToken = "123e4567-e89b-12d3-a456-426614174002"

var restoreReceiptTestKey = []byte("01234567890123456789012345678901")

func rememberRestoreBasisForTest(t *testing.T, apiEnv, secrets map[string]string) {
	t.Helper()
	previous := qualificationRestoreBasis.Load()
	t.Cleanup(func() { qualificationRestoreBasis.Store(previous) })
	if err := rememberQualificationRestoreBasis(apiEnv, secrets); err != nil {
		t.Fatal(err)
	}
}

// A restored guest attests exactly what a boot receipt with the same control
// would, so the host's expectation for the restore request is unchanged.
func TestQualificationRestoreReceiptMatchesBootReceipt(t *testing.T) {
	apiEnv := map[string]string{"MODE": "reviewed"}
	secrets := map[string]string{"DATABASE_URL": "secret-value", "API_KEY": "other"}
	rememberRestoreBasisForTest(t, apiEnv, secrets)
	want, err := qualificationConfigReceiptFrame("main", restoreReceiptTestToken, restoreReceiptTestKey, apiEnv, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sent [][]byte
	send := func(frame []byte, _ time.Duration) error {
		sent = append(sent, append([]byte(nil), frame...))
		return nil
	}
	emitQualificationRestoreReceipt(slog.Default(), restoreReceiptTestToken, hex.EncodeToString(restoreReceiptTestKey), send, time.Second, time.Millisecond)
	if len(sent) != 1 || !bytes.Equal(sent[0], want) {
		t.Fatalf("restore receipt = %q, want boot receipt %q", sent, want)
	}
	if strings.Contains(string(sent[0]), "secret-value") || strings.Contains(string(sent[0]), "DATABASE_URL") || strings.Contains(string(sent[0]), "reviewed") {
		t.Fatal("restore receipt leaked configuration values or secret names")
	}
}

// The restore's guest-event channel opens only after the resume ack, so the
// first sends can fail; the receipt must still arrive inside the window.
func TestQualificationRestoreReceiptRetriesUntilChannelOpens(t *testing.T) {
	rememberRestoreBasisForTest(t, map[string]string{}, nil)
	attempts := 0
	send := func([]byte, time.Duration) error {
		attempts++
		if attempts < 4 {
			return errors.New("connection reset")
		}
		return nil
	}
	emitQualificationRestoreReceipt(slog.Default(), restoreReceiptTestToken, hex.EncodeToString(restoreReceiptTestKey), send, time.Second, time.Millisecond)
	if attempts != 4 {
		t.Fatalf("attempts = %d, want 4", attempts)
	}
	attempts = 0
	never := func([]byte, time.Duration) error { attempts++; return errors.New("connection reset") }
	start := time.Now()
	emitQualificationRestoreReceipt(slog.Default(), restoreReceiptTestToken, hex.EncodeToString(restoreReceiptTestKey), never, 20*time.Millisecond, time.Millisecond)
	if attempts < 2 || time.Since(start) > time.Second {
		t.Fatalf("bounded retry made %d attempts in %s", attempts, time.Since(start))
	}
}

// Only a guest that booted with a qualification receipt can re-attest, and a
// malformed control is never answered.
func TestQualificationRestoreReceiptWithheldWithoutBootBasisOrValidControl(t *testing.T) {
	previous := qualificationRestoreBasis.Load()
	t.Cleanup(func() { qualificationRestoreBasis.Store(previous) })
	qualificationRestoreBasis.Store(nil)
	sends := 0
	send := func([]byte, time.Duration) error { sends++; return nil }
	emitQualificationRestoreReceipt(slog.Default(), restoreReceiptTestToken, hex.EncodeToString(restoreReceiptTestKey), send, time.Second, time.Millisecond)
	if sends != 0 {
		t.Fatal("a guest without a boot receipt basis re-attested")
	}
	rememberRestoreBasisForTest(t, map[string]string{}, nil)
	for _, control := range [][2]string{
		{"", hex.EncodeToString(restoreReceiptTestKey)},
		{"00000000-0000-0000-0000-000000000000", hex.EncodeToString(restoreReceiptTestKey)},
		{restoreReceiptTestToken, "zz"},
		{restoreReceiptTestToken, hex.EncodeToString(restoreReceiptTestKey[:16])},
	} {
		emitQualificationRestoreReceipt(slog.Default(), control[0], control[1], send, time.Second, time.Millisecond)
	}
	if sends != 0 {
		t.Fatal("a malformed restore receipt control was answered")
	}
}

// The resume hook hands its receipt control to the emitter only after the
// guest acknowledged the resume.
func TestResumeDispatchesQualificationRestoreReceiptAfterAck(t *testing.T) {
	origHook := runResumeHookFn
	runResumeHookFn = func(int64, []byte, *resumetimingproto.Timings) error { return nil }
	t.Cleanup(func() { runResumeHookFn = origHook })
	type control struct{ token, key string }
	got := make(chan control, 1)
	origEmit := emitQualificationRestoreReceiptFn
	emitQualificationRestoreReceiptFn = func(_ *slog.Logger, token, key string) { got <- control{token, key} }
	t.Cleanup(func() { emitQualificationRestoreReceiptFn = origEmit })

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	guest := os.NewFile(uintptr(fds[0]), "guest-resume")
	host := os.NewFile(uintptr(fds[1]), "host-resume")
	defer func() { _ = host.Close() }()
	key := hex.EncodeToString(restoreReceiptTestKey)
	body, _ := json.Marshal(map[string]any{"hostTimeUnixNano": 1, "entropy": base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"qualificationReceipt": map[string]string{"token": restoreReceiptTestToken, "macKey": key}})
	msg := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], VsockResumeMsgType)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
	copy(msg[8:], body)
	go func() { _, _ = host.Write(msg) }()

	handleResumeConn(guest, slog.Default())
	frame, err := io.ReadAll(host)
	if err != nil || len(frame) == 0 || frame[0] != VsockResumeAckOK {
		t.Fatalf("ack frame = %v, %v", frame, err)
	}
	select {
	case c := <-got:
		if c.token != restoreReceiptTestToken || c.key != key {
			t.Fatalf("receipt control = %+v", c)
		}
	case <-time.After(time.Second):
		t.Fatal("resume with a receipt control did not re-attest")
	}
}
