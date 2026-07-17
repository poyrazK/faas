package githubd

import (
	"errors"
	"testing"
)

func TestVerifyPushSignature(t *testing.T) {
	secret := []byte("super-secret-webhook-key")
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := SignForTest(body, secret)

	tests := []struct {
		name    string
		body    []byte
		header  string
		secret  []byte
		wantErr bool
	}{
		{"valid", body, good, secret, false},
		{"wrong secret", body, good, []byte("another-secret"), true},
		{"tampered body", []byte(`{"ref":"refs/heads/evil"}`), good, secret, true},
		{"empty secret", body, good, nil, true},
		{"missing sha256= prefix", body, "hex-deadbeef", secret, true},
		{"bad hex", body, "sha256=zzz", secret, true},
		{"empty header", body, "", secret, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyPushSignature(tt.body, tt.header, tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecodePush(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","after":"deadbeef","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	ev, err := DecodePush(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Ref != "refs/heads/main" {
		t.Errorf("ref = %q, want refs/heads/main", ev.Ref)
	}
	if ev.Repository.FullName != "octo/api" {
		t.Errorf("full_name = %q, want octo/api", ev.Repository.FullName)
	}
	_, err = DecodePush([]byte(`{}`))
	if err == nil {
		t.Error("empty body should error")
	}
	_, err = DecodePush([]byte("not-json"))
	if err == nil {
		t.Error("non-json body should error")
	}
	_, err = DecodePush(nil)
	if err == nil {
		t.Error("nil body should error")
	}
}

func TestRefToBranch(t *testing.T) {
	if got := refToBranch("refs/heads/main"); got != "main" {
		t.Errorf("refs/heads/main → %q, want main", got)
	}
	if got := refToBranch("refs/heads/feature/foo"); got != "feature/foo" {
		t.Errorf("refs/heads/feature/foo → %q, want feature/foo", got)
	}
	if got := refToBranch("refs/tags/v1.0"); got != "" {
		t.Errorf("tag → %q, want empty", got)
	}
	if got := refToBranch(""); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
}

// Sentinel check — IsNoBinding must return true for the typed
// error and false for unrelated errors.
func TestIsNoBinding(t *testing.T) {
	if !IsNoBinding(ErrNoBinding) {
		t.Error("IsNoBinding(ErrNoBinding) = false, want true")
	}
	if IsNoBinding(errors.New("other")) {
		t.Error("IsNoBinding(other) = true, want false")
	}
}
