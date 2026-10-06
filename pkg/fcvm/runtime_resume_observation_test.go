package fcvm

// adr: 595 These Unix-socket peers simulate Firecracker and the guest. The
// acknowledged wire bytes and transport failures are real; no KVM is involved.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeResumeAcknowledgmentRequiresAcceptedCommand(t *testing.T) {
	for _, tc := range []struct {
		status int
		cancel bool
	}{
		{http.StatusNoContent, false},
		{http.StatusConflict, false},
		{http.StatusInternalServerError, false},
		{http.StatusNoContent, true},
	} {
		t.Run(fmt.Sprintf("%d-cancel-%t", tc.status, tc.cancel), func(t *testing.T) {
			v := NewJailerVMM(shortChrootBase(t, "r-ack"), time.Second)
			v.fcName = "f"
			l := Lease{Instance: "owner"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			frames := make(chan []byte, 2)
			bindTestSocket(t, v.socketPath(l.Instance), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != http.MethodPatch || r.URL.Path != "/vm" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				frames <- body
				if tc.cancel {
					cancel()
				}
				w.WriteHeader(tc.status)
			}))
			started := time.Now().UnixNano()
			observed, err := v.resumeVMObserved(ctx, l)
			var frame []byte
			select {
			case frame = <-frames:
			case <-time.After(2 * time.Second):
				t.Fatal("resume request did not reach the API peer")
			}
			if string(frame) != `{"state":"Resumed"}` {
				t.Fatalf("delivered command %q", frame)
			}
			if tc.status != http.StatusNoContent || tc.cancel {
				if err == nil || observed != (runtimeResumeCommandAcknowledgment{}) {
					t.Fatalf("unacknowledged resume returned evidence: %+v %v", observed, err)
				}
				if tc.status == http.StatusConflict && v.ResumeVM(t.Context(), l) != nil {
					t.Fatal("legacy conflict retry changed")
				}
				return
			}
			hash := sha256.Sum256(append([]byte("gregale.runtime-resume.command.v1\x00"), frame...))
			if err != nil || observed.Version != 1 || observed.CommandHash != hex.EncodeToString(hash[:]) || observed.CompletedAtUnixNano < started || observed.CompletedAtUnixNano > time.Now().UnixNano() {
				t.Fatalf("acknowledged command: %+v %v", observed, err)
			}
		})
	}
}

type resumeHookPeerResult struct {
	frame []byte
	acked int64
	err   error
}

func captureResumeHookPeer(c net.Conn, ack byte, drop bool, cancel context.CancelFunc) (result resumeHookPeerResult) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(c)
	connect, err := reader.ReadString('\n')
	if err != nil || connect != fmt.Sprintf("CONNECT %d\n", resumeHookGuestPort) {
		result.err = errors.Join(errors.New("invalid CONNECT"), err)
		return
	}
	if _, result.err = io.WriteString(c, "OK 1073741824\n"); result.err != nil {
		return
	}
	var header [8]byte
	if _, result.err = io.ReadFull(reader, header[:]); result.err != nil {
		return
	}
	length := binary.BigEndian.Uint32(header[4:])
	if binary.BigEndian.Uint32(header[:4]) != resumeHookMsgResume || length == 0 || length > resumeHookMaxBodyBytes {
		result.err = errors.New("invalid resume frame")
		return
	}
	result.frame = make([]byte, 8+int(length))
	copy(result.frame, header[:])
	if _, result.err = io.ReadFull(reader, result.frame[8:]); result.err != nil {
		return
	}
	if cancel != nil {
		cancel()
		return
	}
	if !drop {
		result.acked = time.Now().UnixNano()
		_, result.err = c.Write([]byte{ack})
	}
	return
}

func TestNativeResumeHookAcknowledgmentRetainsOnlySuccessfulFrame(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attempts  int
		drops     int
		ack       byte
		cancel    bool
		wantProof bool
	}{
		{"ack", 1, 0, 0, false, true},
		{"lost ack then fresh acknowledged entropy", 2, 1, 0, false, true},
		{"nack", 1, 0, 1, false, false},
		{"persistent lost ack", 3, 3, 0, false, false},
		{"cancel after complete request", 1, 0, 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &JailerVMM{chrootBase: shortChrootBase(t, "h-ack"), fcName: "f"}
			l := Lease{Instance: "owner"}
			sock := v.vsockUDSSock(l.Instance)
			if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(3 * time.Second))
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			results := make(chan resumeHookPeerResult, tc.attempts)
			go func() {
				for n := 0; n < tc.attempts; n++ {
					c, err := listener.Accept()
					if err != nil {
						results <- resumeHookPeerResult{err: err}
						return
					}
					var stop context.CancelFunc
					if tc.cancel {
						stop = cancel
					}
					results <- captureResumeHookPeer(c, tc.ack, n < tc.drops, stop)
				}
			}()
			hostClock := time.Now().UnixNano()
			observed, err := v.triggerResumeHookObserved(ctx, l, hostClock)
			var previousEntropy []byte
			var last resumeHookPeerResult
			for n := 0; n < tc.attempts; n++ {
				select {
				case last = <-results:
				case <-time.After(4 * time.Second):
					t.Fatal("hook peer did not finish")
				}
				if last.err != nil {
					t.Fatal(last.err)
				}
				var body struct {
					Clock   int64  `json:"hostTimeUnixNano"`
					Entropy string `json:"entropy"`
				}
				if json.Unmarshal(last.frame[8:], &body) != nil || body.Clock != hostClock {
					t.Fatal("wrong clock in delivered hook frame")
				}
				entropy, decodeErr := base64.StdEncoding.DecodeString(body.Entropy)
				if decodeErr != nil || len(entropy) != resumeHookEntropyBytes || bytes.Equal(entropy, previousEntropy) {
					t.Fatal("each complete attempt must send fresh entropy")
				}
				previousEntropy = entropy
			}
			if !tc.wantProof {
				if err == nil || observed != (runtimeResumeHookAcknowledgment{}) {
					t.Fatalf("failed hook returned evidence: %+v %v", observed, err)
				}
				return
			}
			hash := sha256.Sum256(append([]byte("gregale.runtime-resume.hook.v1\x00"), last.frame...))
			if err != nil || observed.Version != 1 || observed.PayloadHash != hex.EncodeToString(hash[:]) || observed.HostTimeUnixNano != hostClock || observed.CompletedAtUnixNano < last.acked || observed.CompletedAtUnixNano > time.Now().UnixNano() {
				t.Fatalf("successful frame acknowledgment: %+v %v", observed, err)
			}
		})
	}
}

type resumeShortWriter struct{}

func (resumeShortWriter) Write(frame []byte) (int, error) { return len(frame) - 1, nil }

func TestNativeResumeHookRefusesIncompleteFrame(t *testing.T) {
	if err := writeResumeHookFrame(resumeShortWriter{}, []byte("complete request")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write accepted: %v", err)
	}
}
