// adr: 051

package fcvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWaitCharacterizationReportAcceptsGuestInitiatedStream(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fc-char-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "app-1", UID: os.Getuid(), GID: os.Getgid()}
	if err := os.MkdirAll(v.chrootRoot(lease.Instance), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := v.prepareCharacterizationListener(lease); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.closeCharacterizationListener(lease.Instance) })

	want := api.CharacterizationReport{
		ObservedClass: "http", ObservedPort: 8080, ExitCode: -1,
		ListeningAddrs: []string{"0.0.0.0:8080"}, OutboundCount: 2,
	}
	resultCh := make(chan api.CharacterizationReport, 1)
	errCh := make(chan error, 1)
	go func() {
		got, waitErr := v.WaitCharacterizationReport(context.Background(), lease, 2*time.Second)
		if waitErr != nil {
			errCh <- waitErr
			return
		}
		resultCh <- got
	}()

	conn, err := net.DialTimeout("unix", v.characterizationUDSSock(lease.Instance), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	writeCharacterizationFrame(t, conn, VsockCharacterizationMsgType, want)
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if ack[0] != 0 {
		t.Fatalf("ack = %d, want 0", ack[0])
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	case got := <-resultCh:
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("report = %+v, want %+v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitCharacterizationReport did not return")
	}
	if _, err := os.Stat(v.characterizationUDSSock(lease.Instance)); !os.IsNotExist(err) {
		t.Fatalf("characterization listener survived receipt: %v", err)
	}
	cached, err := v.WaitCharacterizationReport(context.Background(), lease, time.Second)
	if err != nil {
		t.Fatalf("read cached receipt: %v", err)
	}
	if !reflect.DeepEqual(cached, want) {
		t.Fatalf("cached report = %+v, want %+v", cached, want)
	}
}

func TestWaitCharacterizationReportRetriesAfterInvalidFrame(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fc-char-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	v := NewJailerVMM(base, time.Second)
	lease := Lease{Instance: "app-2", UID: os.Getuid(), GID: os.Getgid()}
	if err := os.MkdirAll(v.chrootRoot(lease.Instance), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := v.prepareCharacterizationListener(lease); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.closeCharacterizationListener(lease.Instance) })

	resultCh := make(chan api.CharacterizationReport, 1)
	errCh := make(chan error, 1)
	go func() {
		got, waitErr := v.WaitCharacterizationReport(context.Background(), lease, 2*time.Second)
		if waitErr != nil {
			errCh <- waitErr
			return
		}
		resultCh <- got
	}()

	bad, err := net.DialTimeout("unix", v.characterizationUDSSock(lease.Instance), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	writeCharacterizationFrame(t, bad, VsockCharacterizationMsgType+1, api.CharacterizationReport{ObservedClass: "http"})
	var ack [1]byte
	if _, err := io.ReadFull(bad, ack[:]); err != nil {
		t.Fatal(err)
	}
	_ = bad.Close()
	if ack[0] == 0 {
		t.Fatal("invalid frame received success ack")
	}

	want := api.CharacterizationReport{ObservedClass: "grpc", ObservedPort: 9000, ExitCode: -1}
	good, err := net.DialTimeout("unix", v.characterizationUDSSock(lease.Instance), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	writeCharacterizationFrame(t, good, VsockCharacterizationMsgType, want)
	if _, err := io.ReadFull(good, ack[:]); err != nil {
		t.Fatal(err)
	}
	_ = good.Close()
	if ack[0] != 0 {
		t.Fatalf("valid retry ack = %d, want 0", ack[0])
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	case got := <-resultCh:
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("report = %+v, want %+v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitCharacterizationReport did not accept retry")
	}
}

func TestReadCharacterizationEnvelopeRejectsTrailingJSON(t *testing.T) {
	body := []byte(`{"observed_class":"http"}{}`)
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], VsockCharacterizationMsgType)
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(body)))
	if _, err := readCharacterizationEnvelope(io.MultiReader(bytes.NewReader(hdr[:]), bytes.NewReader(body))); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestNormalizeCharacterizationReportDerivesAuthoritativeClass(t *testing.T) {
	tests := []struct {
		name       string
		report     api.CharacterizationReport
		wantClass  string
		wantErr    bool
		bootReady  bool
		bootFailed bool
	}{
		{name: "bound defaults http", report: api.CharacterizationReport{ObservedClass: "job", ObservedPort: 8080, ExitCode: -1}, wantClass: "http"},
		{name: "bound graphql refinement", report: api.CharacterizationReport{ObservedClass: "graphql", ObservedPort: 8080, ExitCode: -1}, wantClass: "graphql"},
		{name: "clean no-bind is job", report: api.CharacterizationReport{ObservedClass: "http", ExitCode: 0}, wantClass: "job", bootReady: true},
		{name: "running no-bind is worker", report: api.CharacterizationReport{ObservedClass: "grpc", ExitCode: -1}, wantClass: "worker", bootReady: true},
		{name: "failed no-bind has no class", report: api.CharacterizationReport{ObservedClass: "worker", ExitCode: 17, LogTail: "boom"}, bootReady: true, bootFailed: true},
		{name: "invalid port", report: api.CharacterizationReport{ObservedPort: 70000, ExitCode: -1}, wantErr: true},
		{name: "invalid mode", report: api.CharacterizationReport{ObservedPort: 8080, ExitCode: -1, PortNormalizationMode: "magic"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := tt.report
			err := normalizeCharacterizationReport(&report)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalize error = %v, wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if report.ObservedClass != tt.wantClass {
				t.Fatalf("class = %q, want %q", report.ObservedClass, tt.wantClass)
			}
			terminal, bootErr := characterizationBootOutcome(report, nil)
			if terminal != tt.bootReady || (bootErr != nil) != tt.bootFailed {
				t.Fatalf("boot outcome = (terminal=%v, err=%v), want (terminal=%v, failed=%v)", terminal, bootErr, tt.bootReady, tt.bootFailed)
			}
		})
	}
}

func TestReadCharacterizationEnvelopeRejectsNonObject(t *testing.T) {
	for _, body := range [][]byte{[]byte(`null`), []byte(`{}`)} {
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[:4], VsockCharacterizationMsgType)
		binary.BigEndian.PutUint32(hdr[4:], uint32(len(body)))
		if _, err := readCharacterizationEnvelope(io.MultiReader(bytes.NewReader(hdr[:]), bytes.NewReader(body))); err == nil {
			t.Fatalf("invalid characterization body %s accepted", body)
		}
	}
}

func writeCharacterizationFrame(t *testing.T, w io.Writer, msgType uint32, report api.CharacterizationReport) {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], msgType)
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(body)))
	if _, err := w.Write(append(hdr[:], body...)); err != nil {
		t.Fatal(err)
	}
}
