package secretbox

import (
	"path/filepath"
	"sync"
	"testing"

	"filippo.io/age"
)

func TestUnsealIdentityLabel(t *testing.T) {
	for path, want := range map[string]string{
		"/etc/faas/secrets/fleet.age":                                 "fleet",
		"/etc/faas/secrets/fleet.age.previous":                        "fleet",
		"/etc/faas/secrets/fleet.age_previous":                        "fleet",
		"/run/credentials/faas-vmmd.service/faas_fleet_age_identity":  "fleet",
		"/run/credentials/faas-vmmd.service/fleet.age":                "fleet",
		"/etc/faas/secrets/host.age":                                  "host",
		"/run/credentials/faas-apid.service/faas_host_age_identity":   "host",
		"/etc/faas/secrets/domains/runtime.age":                       "runtime",
		"/etc/faas/secrets/domains/runtime.age.previous":              "runtime",
		"/run/credentials/faas-imaged.service/faas_seal_domain_build": "build",
		"/tmp/Some Key.AGE":                                           UnsealIdentityOther,
		"/tmp/" + string(make([]byte, 40)):                            UnsealIdentityOther,
	} {
		if got := UnsealIdentityLabel(path); got != want {
			t.Errorf("UnsealIdentityLabel(%q) = %q, want %q", path, got, want)
		}
	}
}

type unsealCall struct {
	identity string
	ok       bool
}

type recordingObserver struct {
	mu    sync.Mutex
	calls []unsealCall
}

func (o *recordingObserver) SecretUnsealed(identity string, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, unsealCall{identity, ok})
}

func (o *recordingObserver) take() []unsealCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.calls
	o.calls = nil
	return out
}

// TestUnsealTelemetry pins ADR 20261024014500: each decryption reports the
// key file that opened it, a decryption no key opens reports "none", and a
// key not loaded from a file reports "unknown".
func TestUnsealTelemetry(t *testing.T) {
	observer := &recordingObserver{}
	SetUnsealObserver(observer)
	t.Cleanup(func() { SetUnsealObserver(nil) })

	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	path := filepath.Join(t.TempDir(), "runtime.age")
	if err := WriteHostKeyAtPath(path, newID()); err != nil {
		t.Fatal(err)
	}
	runtimeID, err := LoadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	inMemory, stranger := newID(), newID()

	env, err := Seal(runtimeID.Recipient(), Envelope{"KEY": "value"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := OpenMulti([]*age.X25519Identity{stranger, runtimeID}, env); err != nil || got["KEY"] != "value" {
		t.Fatalf("OpenMulti = %v, %v", got, err)
	}
	if _, err := OpenMulti([]*age.X25519Identity{stranger}, env); err == nil {
		t.Fatal("a stranger's key opened the envelope")
	}
	bytesBlob, err := SealBytes(inMemory.Recipient(), "ns", []byte("payload"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, plain, err := OpenBytesMulti([]*age.X25519Identity{inMemory}, bytesBlob); err != nil || string(plain) != "payload" {
		t.Fatalf("OpenBytesMulti = %q, %v", plain, err)
	}

	want := []unsealCall{{"runtime", true}, {UnsealIdentityNone, false}, {UnsealIdentityUnknown, true}}
	got := observer.take()
	if len(got) != len(want) {
		t.Fatalf("observed %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("observed %+v, want %+v", got, want)
		}
	}

	SetUnsealObserver(nil)
	if _, err := OpenMulti([]*age.X25519Identity{runtimeID}, env); err != nil {
		t.Fatalf("open without an observer: %v", err)
	}
	if got := observer.take(); len(got) != 0 {
		t.Fatalf("a removed observer saw %+v", got)
	}
}
