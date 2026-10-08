// adr:683
package fcvm

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

func imageReadinessVMM(t *testing.T, reply func(healthcheckproto.Request) healthcheckproto.Response) (*JailerVMM, Lease) {
	return imageHealthcheckTestSocket(t, func(kind uint32, request healthcheckproto.Request) (uint32, any) {
		if kind == healthcheckproto.ConfigProbe {
			return healthcheckproto.ConfigAck, healthcheckproto.Config{Nonce: request.Nonce, RuntimeID: "main", IntervalNS: int64(time.Millisecond), TimeoutNS: int64(time.Second), Retries: 3}
		}
		response := reply(request)
		response.RuntimeID = "main"
		return healthcheckproto.Ack, response
	})
}

func imageHealthcheckTestSocket(t *testing.T, reply func(uint32, healthcheckproto.Request) (uint32, any)) (*JailerVMM, Lease) {
	t.Helper()
	root, err := os.MkdirTemp("", "image-hc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	v := &JailerVMM{chrootBase: root, readyTimeout: 150 * time.Millisecond}
	l := Lease{Instance: "a"}
	path := v.VsockUDSSocketPath(l.Instance)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil || line != "CONNECT 1028\n" {
					return
				}
				if _, err := conn.Write([]byte("OK 1\n")); err != nil {
					return
				}
				var header [8]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					return
				}
				var request healthcheckproto.Request
				if err := healthcheckproto.ReadBody(conn, binary.BigEndian.Uint32(header[4:]), &request); err != nil {
					return
				}
				kind, response := reply(binary.BigEndian.Uint32(header[:4]), request)
				_ = healthcheckproto.Write(conn, kind, response)
			}()
		}
	}()
	v.tcpReadinessDial = func(string, string, time.Duration) (net.Conn, error) {
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	return v, l
}

func TestImageHealthcheckGatesNetworkAndWorkerReadiness(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeWorker} {
		for _, healthy := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{true: "pass", false: "fail"}[healthy], func(t *testing.T) {
				v, l := imageReadinessVMM(t, func(req healthcheckproto.Request) healthcheckproto.Response {
					return healthcheckproto.Response{Nonce: req.Nonce, Healthy: healthy}
				})
				var receipt *characterizationReceipt
				if mode == api.ExecutionModeWorker {
					done := make(chan struct{})
					close(done)
					receipt = &characterizationReceipt{done: done, report: api.CharacterizationReport{ObservedClass: "worker", ExitCode: -1}}
				}
				err := v.waitApplicationReady(t.Context(), l, "", false, "", 0, mode, true, receipt)
				if (err == nil) != healthy {
					t.Fatalf("healthy=%v readiness error=%v", healthy, err)
				}
			})
		}
	}
}

func TestImageHealthcheckRejectsSnapshotPassAndMissingReports(t *testing.T) {
	var oldNonce string
	var nonceMu sync.Mutex
	v, l := imageReadinessVMM(t, func(req healthcheckproto.Request) healthcheckproto.Response {
		nonceMu.Lock()
		defer nonceMu.Unlock()
		if oldNonce == "" {
			oldNonce = req.Nonce
		}
		return healthcheckproto.Response{Nonce: oldNonce, Healthy: true}
	})
	if err := v.WaitImageHealthcheck(t.Context(), l, 0); err != nil {
		t.Fatal(err)
	}
	// The second boot/restore gets a different host nonce, even for this same instance.
	if err := v.WaitImageHealthcheck(t.Context(), l, 0); err == nil {
		t.Fatal("restored old health result authorized readiness")
	}
	missing := &JailerVMM{chrootBase: t.TempDir(), readyTimeout: 30 * time.Millisecond, tcpReadinessDial: v.tcpReadinessDial}
	if err := missing.waitApplicationReady(t.Context(), l, "", false, "", 0, "", true, nil); err == nil || !strings.Contains(err.Error(), "image_healthcheck") {
		t.Fatalf("TCP success bypassed missing command result: %v", err)
	}
	if err := missing.waitApplicationReady(t.Context(), l, "", false, "", 0, "", false, nil); err != nil {
		t.Fatalf("healthcheck-free legacy readiness broken: %v", err)
	}
}

func TestImageHealthcheckProbeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probeImageHealthcheck(ctx, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("canceled probe passed")
	}
}
