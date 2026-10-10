//go:build linux

// Real pidfd/Unix peer and hook framing, with a modeled guest response.
package fcvm

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeQualificationRestoreResumePinsOriginalPeerAndNeverRetriesPayload(t *testing.T) {
	for _, outcome := range []string{"success", "lost_ack", "wrong_ack", "changed_pid", "changed_start", "changed_uid", "changed_gid", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			base, err := os.MkdirTemp("/tmp", "nrhook-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(base) })
			v := &JailerVMM{chrootBase: base, fcName: "f"}
			owner := nativeLaunchRecord{Authorized: true, PID: os.Getpid(), Lease: Lease{Instance: "target", UID: os.Geteuid(), GID: os.Getegid()}}
			owner.StartTime, err = nativeHostHelperStartTime(owner.PID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(v.chrootRoot(owner.Lease.Instance), 0o700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(v.chrootRoot(owner.Lease.Instance), VsockUDSSocketName))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			var payloads atomic.Int32
			done := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					done <- nil
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(time.Second))
				line, err := bufio.NewReader(connection).ReadString('\n')
				if err != nil {
					done <- nil
					return
				} // Refusal before handshake is expected.
				if line != "CONNECT 1024\n" {
					done <- errors.New("native hook changed original port")
					return
				}
				if _, err := connection.Write([]byte("OK 1\n")); err != nil {
					done <- err
					return
				}
				var header [8]byte
				if _, err := io.ReadFull(connection, header[:]); err != nil {
					done <- err
					return
				}
				length := binary.BigEndian.Uint32(header[4:])
				if binary.BigEndian.Uint32(header[:4]) != resumeHookMsgResume || length > resumeHookMaxBodyBytes {
					done <- errors.New("native hook payload framing changed")
					return
				}
				body := make([]byte, length)
				if _, err := io.ReadFull(connection, body); err != nil {
					done <- err
					return
				}
				var payload struct {
					Entropy  string `json:"entropy"`
					HostTime int64  `json:"hostTimeUnixNano"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					done <- err
					return
				}
				entropy, err := base64.StdEncoding.DecodeString(payload.Entropy)
				if err != nil || len(entropy) != resumeHookEntropyBytes || payload.HostTime <= 0 {
					done <- errors.New("native hook lost fresh entropy or clock")
					return
				}
				payloads.Add(1)
				if outcome == "lost_ack" {
					done <- nil
					return
				}
				ack := byte(0)
				if outcome == "wrong_ack" {
					ack = 1
				}
				response := []byte{ack}
				if ack == 0 {
					response = append(response, resumeCapUserspaceReseed)
				}
				_, err = connection.Write(response)
				done <- err
			}()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			switch outcome {
			case "changed_pid":
				owner.PID = os.Getppid()
				owner.StartTime, err = nativeHostHelperStartTime(owner.PID)
				if err != nil {
					t.Fatal(err)
				}
			case "changed_start":
				owner.StartTime++
			case "changed_uid":
				owner.Lease.UID++
			case "changed_gid":
				owner.Lease.GID++
			case "canceled":
				cancel()
			}
			err = (linuxNativeQualificationRestoreResume{}).Resume(ctx, v, owner)
			if (err == nil) != (outcome == "success") {
				t.Fatal(outcome, err)
			}
			_ = listener.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if outcome == "success" || outcome == "lost_ack" || outcome == "wrong_ack" {
				want = 1
			}
			if payloads.Load() != want {
				t.Fatal("native hook reached an unowned peer or replayed an uncertain payload", payloads.Load())
			}
		})
	}
}
