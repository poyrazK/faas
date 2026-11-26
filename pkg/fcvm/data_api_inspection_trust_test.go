// adr: 650
package fcvm

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

type inspectionTrustSource []byte

func (s inspectionTrustSource) DataAPIInspectionCA() []byte { return s }

type inspectionTrustVMM struct {
	*fakeVMM
	ca       []byte
	observed chan apptaskproto.Request
}

func (v *inspectionTrustVMM) DataAPIInspectionCA() []byte { return v.ca }
func (v *inspectionTrustVMM) DialAppTask(ctx context.Context, _ Lease) (*AppTaskSession, error) {
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		_ = apptaskproto.Serve(ctx, guest, func(_ context.Context, req apptaskproto.Request, _, _ *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
			v.observed <- req
			code := 0
			return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &code}, nil
		})
	}()
	return NewAppTaskSession(host)
}

func TestPrepareDataAPIInspectionRequestUsesOnlyHostTrust(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := inspectionTrustSource(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	req := apptaskproto.Request{Version: apptaskproto.DataAPIInspectionVersion}
	got, err := prepareDataAPIInspectionRequest(req, ca)
	if err != nil || len(got.DataAPIInspectionCA) == 0 {
		t.Fatalf("host CA not delivered: %v", err)
	}
	ca[0] ^= 1
	if got.DataAPIInspectionCA[0] == ca[0] {
		t.Fatal("host CA was not copied")
	}
	if _, err := prepareDataAPIInspectionRequest(req, nil); !errors.Is(err, ErrAppTaskNotConfigured) {
		t.Fatalf("missing source: %v", err)
	}
	if _, err := prepareDataAPIInspectionRequest(req, inspectionTrustSource("invalid")); !errors.Is(err, ErrAppTaskNotConfigured) {
		t.Fatalf("invalid host trust: %v", err)
	}
	req.DataAPIInspectionCA = []byte("caller-supplied")
	if _, err := prepareDataAPIInspectionRequest(req, ca); !errors.Is(err, apptaskproto.ErrInvalidRequest) {
		t.Fatalf("caller trust accepted: %v", err)
	}
	ordinary := apptaskproto.Request{Version: apptaskproto.Version}
	if got, err := prepareDataAPIInspectionRequest(ordinary, nil); err != nil || got.Version != ordinary.Version || len(got.DataAPIInspectionCA) != 0 {
		t.Fatalf("ordinary task changed: %+v %v", got, err)
	}
}

func TestManagerDeliversDataAPIInspectionTrustToGuest(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	vmm := &inspectionTrustVMM{fakeVMM: &fakeVMM{}, ca: ca, observed: make(chan apptaskproto.Request, 1)}
	manager := NewManager(&fakeRunner{}, vmm, Paths{}, "1.0.0", nil, nil)
	lease, err := manager.alloc.Acquire("inspection-task")
	if err != nil {
		t.Fatal(err)
	}
	manager.live["inspection-task"] = &Instance{Lease: lease, AppTaskOnly: true}
	req := apptaskproto.Request{Version: apptaskproto.DataAPIInspectionVersion, TaskID: "inspection-task", Command: []string{dataapiinspectioncommand.Command, "notes", strings.Repeat("a", 64)}, TimeoutSeconds: 5, MaxOutputBytes: 4096}
	result, err := manager.ExecuteAppTask(t.Context(), "inspection-task", req)
	if err != nil || result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("inspection dispatch: %+v %v", result, err)
	}
	got := <-vmm.observed
	if !bytes.Equal(got.DataAPIInspectionCA, ca) || len(req.DataAPIInspectionCA) != 0 || got.Version != apptaskproto.DataAPIInspectionVersion {
		t.Fatal("host CA was not delivered solely to the guest request")
	}
}
