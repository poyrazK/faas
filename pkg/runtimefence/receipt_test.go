package runtimefence

// adr: 622

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func fenceFixture(t *testing.T) (*Verifier, Intent, Claim, ed25519.PrivateKey, time.Time) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	i := Intent{ID: uuid.NewString(), WithdrawalID: uuid.NewString(), AuthorityID: uuid.NewString(), Challenge: uuid.NewString(), GatewayRevision: uuid.NewString(), PublicRevision: uuid.NewString(), SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64), MachineID: strings.Repeat("1", 32), BootID: uuid.NewString(), ResourceID: "provider:host/123", ScopeSHA256: strings.Repeat("b", 64), CreatedAtMicros: now.Add(-2 * time.Minute).UnixMicro()}
	digest, err := IntentDigest(i)
	if err != nil {
		t.Fatal(err)
	}
	c := Claim{Version: 1, Contract: Contract, AuthorityID: i.AuthorityID, ReceiptID: uuid.NewString(), IntentID: i.ID, IntentSHA256: digest, Challenge: i.Challenge, EnforcedAtMicros: now.Add(-time.Second).UnixMicro(), IssuedAtMicros: now.UnixMicro()}
	v, err := NewVerifier(i.AuthorityID, public)
	if err != nil {
		t.Fatal(err)
	}
	return v, i, c, private, now
}

func signedEnvelope(t *testing.T, c Claim, key ed25519.PrivateKey, domain string) []byte {
	t.Helper()
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Envelope{Claim: c, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte(domain), payload...)))})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFenceReceiptBindsCanonicalSignedIntentAndOwnsBytes(t *testing.T) {
	v, i, c, key, now := fenceFixture(t)
	raw := signedEnvelope(t, c, key, SignatureDomain)
	want := bytes.Clone(raw)
	got, err := v.Verify(i, now, raw)
	if err != nil || got.Claim() != c || !bytes.Equal(got.Envelope(), want) || !Digest(got.SHA256()) {
		t.Fatal(got, err)
	}
	raw[0] = '!'
	copyOut := got.Envelope()
	copyOut[0] = '!'
	if !bytes.Equal(got.Envelope(), want) {
		t.Fatal("caller altered accepted evidence")
	}
}

func TestFenceReceiptRejectsEveryDifferentReviewedIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*Intent){
		"intent": func(i *Intent) { i.ID = uuid.NewString() }, "withdrawal": func(i *Intent) { i.WithdrawalID = uuid.NewString() }, "authority": func(i *Intent) { i.AuthorityID = uuid.NewString() }, "challenge": func(i *Intent) { i.Challenge = uuid.NewString() },
		"gateway-head": func(i *Intent) { i.GatewayRevision = uuid.NewString() }, "public-head": func(i *Intent) { i.PublicRevision = uuid.NewString() }, "slot": func(i *Intent) { i.SlotID = uuid.NewString() }, "session": func(i *Intent) { i.SessionID = uuid.NewString() },
		"config": func(i *Intent) { i.ConfigSHA256 = strings.Repeat("c", 64) }, "machine": func(i *Intent) { i.MachineID = strings.Repeat("2", 32) }, "boot": func(i *Intent) { i.BootID = uuid.NewString() }, "resource": func(i *Intent) { i.ResourceID = "provider:host/other" }, "scope": func(i *Intent) { i.ScopeSHA256 = strings.Repeat("d", 64) }, "time": func(i *Intent) { i.CreatedAtMicros++ },
	} {
		t.Run(name, func(t *testing.T) {
			v, i, c, key, now := fenceFixture(t)
			raw := signedEnvelope(t, c, key, SignatureDomain)
			mutate(&i)
			if got, err := v.Verify(i, now, raw); !errors.Is(err, ErrUnverified) || !reflect.DeepEqual(got, VerifiedReceipt{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFenceReceiptRejectsTemporaryIncompleteForeignAndBadClockClaims(t *testing.T) {
	for name, mutate := range map[string]func(*Claim, Intent, time.Time){
		"power-off": func(c *Claim, _ Intent, _ time.Time) { c.Contract = "host_powered_off" }, "network-only": func(c *Claim, _ Intent, _ time.Time) { c.Contract = "network_isolated" }, "mask": func(c *Claim, _ Intent, _ time.Time) { c.Contract = "systemd_masked" }, "lease": func(c *Claim, _ Intent, _ time.Time) { c.Contract = "leased_host_fence" },
		"version": func(c *Claim, _ Intent, _ time.Time) { c.Version = 2 }, "issuer": func(c *Claim, _ Intent, _ time.Time) { c.AuthorityID = uuid.NewString() }, "zero-receipt": func(c *Claim, _ Intent, _ time.Time) { c.ReceiptID = uuid.Nil.String() }, "upper-receipt": func(c *Claim, _ Intent, _ time.Time) { c.ReceiptID = strings.ToUpper(c.ReceiptID) }, "challenge": func(c *Claim, _ Intent, _ time.Time) { c.Challenge = uuid.NewString() }, "intent": func(c *Claim, _ Intent, _ time.Time) { c.IntentID = uuid.NewString() }, "digest": func(c *Claim, _ Intent, _ time.Time) { c.IntentSHA256 = strings.Repeat("f", 64) },
		"before-review": func(c *Claim, i Intent, _ time.Time) { c.EnforcedAtMicros = i.CreatedAtMicros - 1 }, "issue-before-enforced": func(c *Claim, _ Intent, _ time.Time) { c.IssuedAtMicros = c.EnforcedAtMicros - 1 }, "future": func(c *Claim, _ Intent, now time.Time) { c.IssuedAtMicros = now.Add(time.Microsecond).UnixMicro() }, "expired": func(c *Claim, i Intent, now time.Time) {
			c.EnforcedAtMicros = i.CreatedAtMicros
			c.IssuedAtMicros = now.Add(-api.RuntimeUpgradeExternalFenceMaxAge - time.Microsecond).UnixMicro()
		},
	} {
		t.Run(name, func(t *testing.T) {
			v, i, c, key, now := fenceFixture(t)
			mutate(&c, i, now)
			got, err := v.Verify(i, now, signedEnvelope(t, c, key, SignatureDomain))
			if !errors.Is(err, ErrUnverified) || !reflect.DeepEqual(got, VerifiedReceipt{}) {
				t.Fatal(got, err)
			}
		})
	}
	v, i, c, key, now := fenceFixture(t)
	c.EnforcedAtMicros = i.CreatedAtMicros
	c.IssuedAtMicros = now.Add(-api.RuntimeUpgradeExternalFenceMaxAge).UnixMicro()
	if _, err := v.Verify(i, now, signedEnvelope(t, c, key, SignatureDomain)); err != nil {
		t.Fatal("exact age bound", err)
	}
}

func TestFenceReceiptRejectsNoncanonicalAndAmbiguousWire(t *testing.T) {
	for name, mutate := range map[string]func([]byte) []byte{
		"newline": func(b []byte) []byte { return append(b, '\n') }, "space": func(b []byte) []byte { return append([]byte(" "), b...) }, "truncated": func(b []byte) []byte { return b[:len(b)-1] }, "trailing": func(b []byte) []byte { return append(b, []byte("{}")...) },
		"duplicate-top": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), "{\"claim\":", "{\"signature\":\"ignored\",\"claim\":", 1))
		},
		"duplicate-nested": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), "\"version\":1", "\"version\":1,\"version\":1", 1))
		},
		"unknown": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), "\"version\":1", "\"unknown\":0,\"version\":1", 1))
		},
		"alias": func(b []byte) []byte { return []byte(strings.Replace(string(b), "\"claim\"", "\"CLAIM\"", 1)) },
		"wrong-type": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), "\"version\":1", "\"version\":\"1\"", 1))
		},
		"empty": func(_ []byte) []byte { return nil }, "bound": func(_ []byte) []byte {
			return bytes.Repeat([]byte("a"), api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			v, i, c, key, now := fenceFixture(t)
			got, err := v.Verify(i, now, mutate(signedEnvelope(t, c, key, SignatureDomain)))
			if !errors.Is(err, ErrUnverified) || !reflect.DeepEqual(got, VerifiedReceipt{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFenceReceiptRejectsWrongKeyDomainAndSignatureEncoding(t *testing.T) {
	for _, kind := range []string{"key", "domain", "padding", "signature", "noncanonical-bits"} {
		t.Run(kind, func(t *testing.T) {
			v, i, c, key, now := fenceFixture(t)
			domain := SignatureDomain
			if kind == "key" {
				_, other, err := ed25519.GenerateKey(nil)
				if err != nil {
					t.Fatal(err)
				}
				key = other
			}
			if kind == "domain" {
				domain = "another-protocol\x00"
			}
			raw := signedEnvelope(t, c, key, domain)
			if kind == "padding" || kind == "signature" || kind == "noncanonical-bits" {
				var envelope Envelope
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "padding":
					envelope.Signature += "=="
				case "signature":
					envelope.Signature = "invalid"
				case "noncanonical-bits":
					envelope.Signature = envelope.Signature[:len(envelope.Signature)-1] + "B"
				}
				var err error
				raw, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got, err := v.Verify(i, now, raw); !errors.Is(err, ErrUnverified) || !reflect.DeepEqual(got, VerifiedReceipt{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFenceVerifierRejectsWeakNoncanonicalAndInvalidAuthorityKeys(t *testing.T) {
	identity := make([]byte, 32)
	identity[0] = 1
	noncanonical, err := hex.DecodeString("edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range [][]byte{nil, make([]byte, 31), make([]byte, 32), identity, noncanonical, bytes.Repeat([]byte{0xff}, 32)} {
		if got, err := NewVerifier(uuid.NewString(), key); !errors.Is(err, ErrUnverified) || got != nil {
			t.Fatal("weak issuer admitted", got, err)
		}
	}
	v, i, c, key, now := fenceFixture(t)
	public := key.Public().(ed25519.PublicKey)
	pinned, err := NewVerifier(i.AuthorityID, public)
	if err != nil {
		t.Fatal(err)
	}
	public[0] ^= 0xff
	if _, err := pinned.Verify(i, now, signedEnvelope(t, c, key, SignatureDomain)); err != nil {
		t.Fatal("caller changed pinned key", err)
	}
	for _, invalid := range []*Verifier{nil, {}} {
		if got, err := invalid.Verify(i, now, signedEnvelope(t, c, key, SignatureDomain)); !errors.Is(err, ErrUnverified) || !reflect.DeepEqual(got, VerifiedReceipt{}) {
			t.Fatal(got, err)
		}
	}
	if _, err := v.Verify(i, time.Time{}, signedEnvelope(t, c, key, SignatureDomain)); !errors.Is(err, ErrUnverified) {
		t.Fatal(err)
	}
}

func TestFenceIntentRejectsUnboundedNoncanonicalHostAndStartupReviews(t *testing.T) {
	for name, mutate := range map[string]func(*Intent){
		"uuid": func(i *Intent) { i.ID = "invalid" }, "zero": func(i *Intent) { i.SessionID = uuid.Nil.String() }, "upper": func(i *Intent) { i.BootID = strings.ToUpper(i.BootID) }, "config": func(i *Intent) { i.ConfigSHA256 = "bad" }, "scope": func(i *Intent) { i.ScopeSHA256 = "bad" }, "machine-zero": func(i *Intent) { i.MachineID = strings.Repeat("0", 32) }, "machine-upper": func(i *Intent) { i.MachineID = strings.Repeat("A", 32) }, "resource-bound": func(i *Intent) { i.ResourceID = strings.Repeat("r", api.RuntimeUpgradeExternalFenceResourceMaxBytes+1) }, "resource-space": func(i *Intent) { i.ResourceID = "r secret" }, "no-created-time": func(i *Intent) { i.CreatedAtMicros = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			_, i, _, _, _ := fenceFixture(t)
			mutate(&i)
			if _, err := IntentDigest(i); !errors.Is(err, ErrUnverified) {
				t.Fatal(err)
			}
		})
	}
}
