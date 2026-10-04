package runtimequota

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func fixture(t *testing.T) (Grant, Signer, Signer, Trust) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cellPub, cellPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directoryID, cellID := uuid.NewString(), uuid.NewString()
	directory, err := NewSigner(directoryID, private)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := NewSigner(cellID, cellPrivate)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := NewTrust([]Anchor{{Role: Directory, IssuerID: directoryID, Public: pub}, {Role: Cell, IssuerID: cellID, Public: cellPub}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := Grant{IssuerID: directoryID, GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Policy: p, Concurrency: 2, RolloutConcurrency: api.RolloutConcurrencyGrant, IssuedAtMicros: time.Now().UnixMicro(), Binding: Binding{CellID: cellID, Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalOrgID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "quota-regional", APIBase: "https://fsn.example.com"}}
	return g, directory, cell, trust
}

func TestRuntimeQuotaProtocolRoundTrip(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	e, err := directory.SignGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyGrant(e, g.Binding); err != nil || got != g {
		t.Fatalf("grant: %+v %v", got, err)
	}
	if again, err := directory.SignGrant(g); err != nil || again != e {
		t.Fatalf("non-deterministic retry: %v", err)
	}
	r := Retirement{Grant: g, RetirementID: uuid.NewString(), RetiredAtMicros: g.IssuedAtMicros + 1}
	re, err := directory.SignRetirement(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyRetirement(re, g.Binding); err != nil || got != r {
		t.Fatalf("retirement: %+v %v", got, err)
	}
	d := Drain{Retirement: r, AdmissionFrozen: true}
	de, err := cell.SignDrain(d)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyDrain(de); err != nil || got != d {
		t.Fatalf("drain: %+v %v", got, err)
	}
	for _, wrong := range []Envelope{re, de} {
		if _, err := trust.VerifyGrant(wrong, g.Binding); err == nil {
			t.Fatal("cross-operation replay")
		}
	}
	if _, err := trust.VerifyDrain(e); err == nil {
		t.Fatal("directory message used as cell drain")
	}
	wrong := g.Binding
	wrong.RegionalOrgID = uuid.NewString()
	if _, err := trust.VerifyGrant(e, wrong); !errors.Is(err, ErrBinding) {
		t.Fatalf("tenant audience: %v", err)
	}
	wrong = g.Binding
	wrong.APIBase = "https://replacement.example.com"
	if _, err := trust.VerifyRetirement(re, wrong); !errors.Is(err, ErrBinding) {
		t.Fatalf("origin audience: %v", err)
	}
}

func TestRuntimeQuotaProtocolStrictEncodingAndTampering(t *testing.T) {
	g, signer, _, trust := fixture(t)
	e, err := signer.SignGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := base64.RawURLEncoding.DecodeString(e.Payload)
	for _, raw := range []string{
		strings.Replace(string(payload), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(payload), `"version":1`, `"version":1,"unknown":true`, 1),
		strings.Replace(string(payload), `"version":1`, `"version":1.0`, 1),
		" " + string(payload), string(payload) + "{}",
	} {
		// The trusted key signs malformed JSON directly to test decoding, rather
		// than relying only on invalid-signature rejection.
		bad := Envelope{KeyID: signer.keyID, Payload: base64.RawURLEncoding.EncodeToString([]byte(raw)), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(signer.private, signedBytes(grantKind, signer.keyID, []byte(raw))))}
		if _, err := trust.VerifyGrant(bad, g.Binding); err == nil {
			t.Fatalf("ambiguous payload accepted: %s", raw)
		}
	}
	for _, mutate := range []func(*Envelope){
		func(e *Envelope) { e.Payload += "=" },
		func(e *Envelope) { e.Signature += "=" },
		func(e *Envelope) { e.KeyID = strings.Repeat("f", 64) },
		func(e *Envelope) { e.Payload = base64.RawURLEncoding.EncodeToString([]byte("{}")) },
		func(e *Envelope) { e.Payload = strings.Repeat("a", api.RegionalRuntimeQuotaEnvelopeMaxBytes+1) },
	} {
		bad := e
		mutate(&bad)
		if _, err := trust.VerifyGrant(bad, g.Binding); err == nil {
			t.Fatal("envelope tampering accepted")
		}
	}
	// Unknown fields in the outer transport envelope are refused by Decode.
	raw, _ := json.Marshal(e)
	if _, err := Decode(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte(strings.TrimSuffix(string(raw), "}") + `,"unknown":true}`)); err == nil {
		t.Fatal("unknown envelope field accepted")
	}
}

func TestRuntimeQuotaProtocolTrustRotationAndRoles(t *testing.T) {
	g, old, _, _ := fixture(t)
	newPub, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewSigner(g.IssuerID, newPrivate)
	if err != nil {
		t.Fatal(err)
	}
	oldPub := old.private.Public().(ed25519.PublicKey)
	trust, err := NewTrust([]Anchor{{Role: Directory, IssuerID: g.IssuerID, Public: oldPub}, {Role: Directory, IssuerID: g.IssuerID, Public: newPub}})
	if err != nil {
		t.Fatal(err)
	}
	for _, signer := range []Signer{old, next} {
		e, err := signer.SignGrant(g)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := trust.VerifyGrant(e, g.Binding); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewTrust([]Anchor{{Role: Directory, IssuerID: g.IssuerID, Public: oldPub}, {Role: Cell, IssuerID: g.Binding.CellID, Public: oldPub}}); !errors.Is(err, ErrTrust) {
		t.Fatalf("key shared across authority roles: %v", err)
	}
	wrong, err := NewTrust([]Anchor{{Role: Directory, IssuerID: uuid.NewString(), Public: oldPub}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := old.SignGrant(g)
	if _, err := wrong.VerifyGrant(e, g.Binding); err == nil {
		t.Fatal("issuer substitution accepted")
	}
	// Constructor copies both private and public key buffers.
	newPrivate[0] ^= 1
	newPub[0] ^= 1
	if e, err := next.SignGrant(g); err != nil {
		t.Fatal(err)
	} else if _, err = trust.VerifyGrant(e, g.Binding); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeQuotaProtocolPoliciesAndDrainConditions(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	for _, mutate := range []func(*Grant){
		func(g *Grant) { g.Concurrency = 0 },
		func(g *Grant) { g.Concurrency = g.Policy.Concurrency + 1 },
		func(g *Grant) { g.Generation = api.GlobalRuntimeQuotaGenerationMax + 1 },
		func(g *Grant) { g.Policy.ID = strings.Repeat("a", 64) },
		func(g *Grant) { g.Binding.APIBase = "https://EXAMPLE.com" },
		func(g *Grant) { g.Binding.RegionalOrgID = uuid.Nil.String() },
	} {
		bad := g
		mutate(&bad)
		if _, err := directory.SignGrant(bad); err == nil {
			t.Fatal("invalid grant signed")
		}
	}
	old := g
	old.Policy.Concurrency++
	old.Policy.PeakConcurrency++
	old.Policy.ID = api.RuntimeQuotaPolicyID(old.Policy.Plan, old.Policy.Concurrency, old.Policy.PeakConcurrency)
	if _, err := directory.SignGrant(old); err == nil {
		t.Fatal("historical policy issued as new grant")
	}
	r := Retirement{Grant: old, RetirementID: uuid.NewString(), RetiredAtMicros: old.IssuedAtMicros + 1}
	e, err := directory.SignRetirement(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = trust.VerifyRetirement(e, old.Binding); err != nil {
		t.Fatal(err)
	}
	for _, d := range []Drain{{Retirement: r}, {Retirement: r, AdmissionFrozen: true, CountedInstances: 1}, {Retirement: r, AdmissionFrozen: true, CountedInstances: -1}} {
		if _, err := cell.SignDrain(d); err == nil {
			t.Fatal("unfenced/nonempty drain signed")
		}
	}
}

func TestRuntimeQuotaProtocolRetirementSurvivesClockRollback(t *testing.T) {
	g, directory, cell, trust := fixture(t)
	// Store timestamps use wall clocks. The generation and retirement UUID
	// establish identity even if that clock moves backwards after issuance.
	r := Retirement{Grant: g, RetirementID: uuid.NewString(), RetiredAtMicros: g.IssuedAtMicros - 1}
	e, err := directory.SignRetirement(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyRetirement(e, g.Binding); err != nil || got != r {
		t.Fatalf("clock rollback stranded retirement: %+v %v", got, err)
	}
	d := Drain{Retirement: r, AdmissionFrozen: true}
	e, err = cell.SignDrain(d)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := trust.VerifyDrain(e); err != nil || got != d {
		t.Fatalf("clock rollback stranded drain: %+v %v", got, err)
	}
}
