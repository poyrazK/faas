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
