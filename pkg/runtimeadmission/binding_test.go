package runtimeadmission

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func validBinding(now time.Time) Binding {
	return Binding{ProtocolVersion: ProtocolVersion, Token: uuid.NewString(), InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(), NodeID: uuid.NewString(), Incarnation: uuid.NewString(), DesiredRevision: 9, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), PayloadHash: strings.Repeat("c", 64), EgressRevision: 7, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
}

func TestBindingExpiryAndValidation(t *testing.T) {
	now := time.Now()
	base := validBinding(now)
	if err := base.Validate(now); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Binding)
		want   error
	}{
		{"version", func(b *Binding) { b.ProtocolVersion++ }, ErrInvalid},
		{"empty token", func(b *Binding) { b.Token = "" }, ErrInvalid},
		{"noncanonical UUID", func(b *Binding) { b.NodeID = strings.ToUpper(b.NodeID) }, ErrInvalid},
		{"zero desired revision", func(b *Binding) { b.DesiredRevision = 0 }, ErrInvalid},
		{"zero egress revision", func(b *Binding) { b.EgressRevision = 0 }, ErrInvalid},
		{"missing hash", func(b *Binding) { b.PayloadHash = "" }, ErrInvalid},
		{"noncanonical hash", func(b *Binding) { b.EffectiveHash = strings.Repeat("A", 64) }, ErrInvalid},
		{"future issued", func(b *Binding) {
			b.IssuedAtUnixNano = now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew + time.Second).UnixNano()
		}, ErrInvalid},
		{"unbounded TTL", func(b *Binding) {
			b.ExpiresAtUnixNano = now.Add(api.ApplicationStandardRuntimeAdmissionTTL + time.Nanosecond).UnixNano()
		}, ErrInvalid},
		{"exact expiry", func(b *Binding) {
			b.IssuedAtUnixNano = now.Add(-time.Minute).UnixNano()
			b.ExpiresAtUnixNano = now.UnixNano()
		}, ErrExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := base
			test.mutate(&b)
			if err := b.Validate(now); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
	decoded, err := BindingFromProto(base.ToProto())
	if err != nil || decoded != base {
		t.Fatal("lossy binding round trip")
	}
}

func TestReceiptRequiresEveryGrantFieldAndValidNativeFacts(t *testing.T) {
	now := time.Now()
	b := validBinding(now)
	r := Receipt{Binding: b, NativeInputHash: strings.Repeat("d", 64), Netns: "fc-runtime", HostIP: "10.100.0.2", LeaseUID: 20000, Method: vmmdpb.WakeMethod_WAKE_RESTORE, CompletedAtUnixNano: now.UnixNano()}
	if err := r.Check(b, now); err != nil {
		t.Fatal(err)
	}
	value := reflect.ValueOf(&b).Elem()
	for i := 0; i < value.NumField(); i++ {
		name := value.Type().Field(i).Name
		t.Run(name, func(t *testing.T) {
			copy := r
			field := reflect.ValueOf(&copy.Binding).Elem().Field(i)
			switch field.Kind() {
			case reflect.String:
				field.SetString(field.String() + "x")
			case reflect.Int64:
				field.SetInt(field.Int() + 1)
			case reflect.Uint32:
				field.SetUint(field.Uint() + 1)
			}
			if err := copy.Check(b, now); err == nil {
				t.Fatal("changed grant accepted")
			}
		})
	}
	for _, mutate := range []func(*Receipt){func(r *Receipt) { r.NativeInputHash = "" }, func(r *Receipt) { r.HostIP = "invalid" }, func(r *Receipt) { r.LeaseUID = 0 }, func(r *Receipt) { r.Netns = "" }, func(r *Receipt) { r.Method = 99 }, func(r *Receipt) { r.CompletedAtUnixNano = b.ExpiresAtUnixNano }} {
		copy := r
		mutate(&copy)
		if err := copy.Check(b, now); err == nil {
			t.Fatal("invalid native facts accepted")
		}
	}
	decoded, err := ReceiptFromProto(r.ToProto())
	if err != nil || !decoded.Equal(r) {
		t.Fatal("lossy receipt round trip")
	}
}
