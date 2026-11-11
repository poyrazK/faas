package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/storage"
)

// adr: 20261110210000 — with a runtime opener (secretd), vmmd opens app
// secrets without any runtime identity of its own.

type fakeOpener struct {
	values map[string]string
	err    error
	calls  []string
}

func (f *fakeOpener) OpenAppSecrets(_ context.Context, appID string, entries []SealedEnvEntry) (map[string]string, error) {
	f.calls = append(f.calls, appID)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]string{}
	for _, e := range entries {
		if v, ok := f.values[e.Key]; ok {
			out[e.Key] = v
		}
	}
	return out, nil
}

func TestRuntimeOpenerOpensWithoutLocalIdentity(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	opener := &fakeOpener{values: map[string]string{"DB_URL": "postgres://x", "API_KEY": "k"}}
	m.SetRuntimeSecretOpener(opener)
	entries := []SealedEnvEntry{{Key: "DB_URL", Ciphertext: []byte("c1")}, {Key: "API_KEY", SourceKey: "KEY", Ciphertext: []byte("c2")}}

	secrets, err := m.UnsealRuntimeSecrets(context.Background(), "app-1", entries, "acct")
	if err != nil || secrets["DB_URL"] != "postgres://x" || secrets["API_KEY"] != "k" {
		t.Fatalf("UnsealRuntimeSecrets = %v, %v", secrets, err)
	}
	files, _, err := m.prepareWakeFiles(context.Background(), WakeRequest{AppID: "app-1", AccountID: "acct", SealedEnvEntries: entries})
	if err != nil || !strings.Contains(string(files), "postgres://x") {
		t.Fatalf("prepareWakeFiles = %s, %v", files, err)
	}
	if strings.Join(opener.calls, ",") != "app-1,app-1" {
		t.Fatalf("opener calls = %v", opener.calls)
	}
}

func TestRuntimeOpenerRefusals(t *testing.T) {
	entries := []SealedEnvEntry{{Key: "DB_URL", Ciphertext: []byte("c1")}, {Key: "API_KEY", Ciphertext: []byte("c2")}}
	for _, tc := range []struct {
		name   string
		opener *fakeOpener
		appID  string
		want   string
	}{
		{name: "partial answer", opener: &fakeOpener{values: map[string]string{"DB_URL": "x"}}, appID: "app-1", want: "returned 1 values for 2 entries"},
		{name: "opener refuses", opener: &fakeOpener{err: errors.New("permission denied")}, appID: "app-1", want: "permission denied"},
		{name: "no app id", opener: &fakeOpener{values: map[string]string{"DB_URL": "x", "API_KEY": "y"}}, want: "needs the app id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			m.SetRuntimeSecretOpener(tc.opener)
			secrets, err := m.UnsealRuntimeSecrets(context.Background(), tc.appID, entries, "acct")
			if err == nil || !strings.Contains(err.Error(), tc.want) || secrets != nil {
				t.Fatalf("UnsealRuntimeSecrets = %v, %v; want %q", secrets, err, tc.want)
			}
			files, _, err := m.prepareWakeFiles(context.Background(), WakeRequest{AppID: tc.appID, AccountID: "acct", SealedEnvEntries: entries})
			if err == nil || files != nil {
				t.Fatalf("prepareWakeFiles = %s, %v; want an error and no file", files, err)
			}
		})
	}
}

// Without an opener and without an identity, nothing opens (unchanged).
func TestNoOpenerNoIdentityStillRefuses(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	_, err := m.UnsealRuntimeSecrets(context.Background(), "app-1", []SealedEnvEntry{{Key: "A", Ciphertext: []byte("c")}}, "acct")
	if !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("err = %v, want ErrNoHostKey", err)
	}
}

type fakeCaptureOpener struct {
	fakeOpener
	key   *age.X25519Identity
	calls []string
}

func (f *fakeCaptureOpener) OpenCaptureKey(_ context.Context, appID, captureID string) (*age.X25519Identity, error) {
	f.calls = append(f.calls, appID+"/"+captureID)
	return f.key, nil
}

// Slice 3d: with a runtime opener that opens capture keys, a sealed capture
// restores without any runtime identity on this node.
func TestSealedCaptureKeyFromRuntimeOpener(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.WithStorage(be)
	opener := &fakeCaptureOpener{key: key}
	m.SetRuntimeSecretOpener(opener)
	snap := &Snapshot{CaptureID: "cap-1", SealedKey: []byte("sealed elsewhere")}
	if err := m.openSealedSnapshot(context.Background(), "app-1", snap); err != nil {
		t.Fatal(err)
	}
	if strings.Join(opener.calls, ",") != "app-1/cap-1" || snap.Storage == nil {
		t.Fatalf("calls = %v, storage = %v", opener.calls, snap.Storage)
	}
	if err := m.openSealedSnapshot(context.Background(), "", &Snapshot{CaptureID: "cap-1", SealedKey: []byte("x")}); err == nil {
		t.Fatal("a capture without its app id opened remotely")
	}
	// Without an opener and without an identity, nothing opens (unchanged).
	local := newTestManager(&fakeRunner{}, &fakeVMM{})
	local.WithStorage(be)
	if err := local.openSealedSnapshot(context.Background(), "app-1", &Snapshot{CaptureID: "cap-1", SealedKey: []byte("x")}); !errors.Is(err, ErrNoCaptureKey) {
		t.Fatalf("local without identity = %v, want ErrNoCaptureKey", err)
	}
}
