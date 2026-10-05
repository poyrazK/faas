package runtimequota

// adr: 618
import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func capacityPermitFixture(g Grant) CapacityPermit {
	return CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: CapacityWarm}
}
func TestCapacityStopProtocol(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	p := capacityPermitFixture(g)
	physical := CapacityStop{Permit: p, FenceID: uuid.NewString(), AdmissionFrozen: true}
	seat := ServingSeatStop{Seat: ServingSeat{Permit: p, ID: uuid.NewString()}, FenceID: uuid.NewString(), AdmissionFrozen: true}
	pe, err := cell.SignCapacityStop(physical)
	if err != nil {
		t.Fatal(err)
	}
	se, err := cell.SignServingSeatStop(seat)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyCapacityStop(pe); err != nil || got != physical {
		t.Fatalf("physical: %+v %v", got, err)
	}
	if got, err := trust.VerifyServingSeatStop(se); err != nil || got != seat {
		t.Fatalf("seat: %+v %v", got, err)
	}
	if _, err = trust.VerifyCapacityStop(se); err == nil {
		t.Fatal("pause used as physical drain")
	}
	if _, err = trust.VerifyServingSeatStop(pe); err == nil {
		t.Fatal("physical used as pause")
	}
	if _, err = directory.SignCapacityStop(physical); err == nil {
		t.Fatal("directory asserted cell stop")
	}
	wrong, _ := NewTrust([]Anchor{{Role: Directory, IssuerID: g.Binding.CellID, Public: cell.private.Public().(ed25519.PublicKey)}})
	if _, err = wrong.VerifyCapacityStop(pe); !errors.Is(err, ErrTrust) {
		t.Fatalf("wrong role: %v", err)
	}
	// Historical policy identity remains closable; it cannot be newly issued.
	old := physical
	old.Permit.Grant.Policy.Concurrency++
	old.Permit.Grant.Policy.PeakConcurrency++
	old.Permit.Grant.Policy.ID = api.RuntimeQuotaPolicyID(old.Permit.Grant.Policy.Plan, old.Permit.Grant.Policy.Concurrency, old.Permit.Grant.Policy.PeakConcurrency)
	if old.Permit.Grant.Validate() == nil {
		t.Fatal("historical policy became current")
	}
	oe, err := cell.SignCapacityStop(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = trust.VerifyCapacityStop(oe); err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewSigner(g.Binding.CellID, private)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewTrust([]Anchor{{Role: Cell, IssuerID: g.Binding.CellID, Public: cell.private.Public().(ed25519.PublicKey)}, {Role: Cell, IssuerID: g.Binding.CellID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	for _, signer := range []Signer{cell, next} {
		e, err := signer.SignServingSeatStop(seat)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = rotated.VerifyServingSeatStop(e); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCapacityStopStrictClaims(t *testing.T) {
	g, _, cell, trust := fixture(t)
	p := capacityPermitFixture(g)
	for _, mutate := range []func(*CapacityPermit){func(p *CapacityPermit) { p.HostEpochID = uuid.Nil.String() }, func(p *CapacityPermit) { p.RunID = strings.ToUpper(p.RunID) }, func(p *CapacityPermit) { p.Kind = "builder" }, func(p *CapacityPermit) { p.NodeID = "" }} {
		bad := p
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid physical identity")
		}
	}
	for _, stop := range []CapacityStop{{Permit: p, FenceID: uuid.NewString()}, {Permit: p, FenceID: uuid.NewString(), AdmissionFrozen: true, CountedRuntimes: 1}, {Permit: p, FenceID: uuid.NewString(), AdmissionFrozen: true, CountedRuntimes: -1}} {
		if _, err := cell.SignCapacityStop(stop); err == nil {
			t.Fatal("nonempty/unfenced stop signed")
		}
	}
	for _, kind := range []string{CapacityPrime, CapacityAppTask} {
		bad := p
		bad.Kind = kind
		if (ServingSeat{Permit: bad, ID: uuid.NewString()}).Validate() == nil {
			t.Fatal("nonserving permit acquired seat")
		}
	}
	for _, kind := range []string{"capacity-stop", "serving-seat-stop"} {
		t.Run(kind, func(t *testing.T) {
			stop := CapacityStop{Permit: p, FenceID: uuid.NewString(), AdmissionFrozen: true}
			var e Envelope
			var err error
			verifyStop := func(e Envelope) error { _, err := trust.VerifyCapacityStop(e); return err }
			if kind == "capacity-stop" {
				e, err = cell.SignCapacityStop(stop)
			} else {
				e, err = cell.SignServingSeatStop(ServingSeatStop{Seat: ServingSeat{Permit: p, ID: uuid.NewString()}, FenceID: stop.FenceID, AdmissionFrozen: true})
				verifyStop = func(e Envelope) error { _, err := trust.VerifyServingSeatStop(e); return err }
			}
			if err != nil {
				t.Fatal(err)
			}
			payload, err := base64.RawURLEncoding.DecodeString(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{strings.Replace(string(payload), `"HostEpochID":`, `"unknown":true,"HostEpochID":`, 1), strings.Replace(string(payload), `"AdmissionFrozen":true`, `"AdmissionFrozen":true,"AdmissionFrozen":true`, 1), " " + string(payload), string(payload) + "{}"} {
				bad := Envelope{KeyID: cell.keyID, Payload: base64.RawURLEncoding.EncodeToString([]byte(raw)), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(cell.private, signedBytes(kind, cell.keyID, []byte(raw))))}
				if err = verifyStop(bad); err == nil {
					t.Fatal("ambiguous signed stop accepted")
				}
			}
			for _, mutate := range []func(*Envelope){func(e *Envelope) { e.Payload += "=" }, func(e *Envelope) { e.Signature += "=" }, func(e *Envelope) { e.Payload = strings.Repeat("a", api.RegionalRuntimeQuotaEnvelopeMaxBytes+1) }} {
				bad := e
				mutate(&bad)
				if verifyStop(bad) == nil {
					t.Fatal("tampered stop accepted")
				}
			}
		})
	}
}
