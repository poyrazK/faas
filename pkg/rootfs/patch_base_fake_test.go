package rootfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// fakeDebugfs answers the commands PatchBaseGuestInit runs without e2fsprogs,
// so every refusal and failure branch runs on any machine.
type fakeDebugfs struct {
	stats    map[string]string // debugfs stat path -> output
	statErr  error
	dump     []byte // bytes a dump request writes
	dumpErr  error
	writeErr error
	fsckErr  error
	calls    []string
}

const (
	statDir     = "Inode: 2   Type: directory    Mode:  0755   Flags: 0x80000\nUser:     0   Group:     0   Project:     0   Size: 4096\n"
	statInit    = "Inode: 12   Type: regular    Mode:  0755   Flags: 0x80000\nUser:   992   Group:   988   Project:     0   Size: 3\n"
	statSymlink = "Inode: 13   Type: symlink    Mode:  0777   Flags: 0x0\nUser:     0   Group:     0   Project:     0   Size: 8\nFast link dest: \"usr/sbin\"\n"
)

func (f *fakeDebugfs) Run(context.Context, []string) error { return nil }

func (f *fakeDebugfs) Output(_ context.Context, argv []string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(argv, " "))
	switch {
	case argv[0] == "env":
		return nil, f.writeErr
	case argv[0] == "e2fsck":
		return nil, f.fsckErr
	case argv[0] == "debugfs" && strings.HasPrefix(argv[2], "stat "):
		if f.statErr != nil {
			return nil, f.statErr
		}
		return []byte(f.stats[strings.TrimPrefix(argv[2], "stat ")]), nil
	case argv[0] == "debugfs" && strings.HasPrefix(argv[2], "dump "):
		if f.dumpErr != nil {
			return nil, f.dumpErr
		}
		fields := strings.Fields(argv[2])
		return nil, os.WriteFile(fields[2], f.dump, 0o644)
	}
	return nil, errors.New("unexpected command " + strings.Join(argv, " "))
}

type recordingSigner struct {
	keys []string
	err  error
}

func (s *recordingSigner) Sign(_ context.Context, layerKey, sigKey string) error {
	s.keys = append(s.keys, layerKey+"->"+sigKey)
	return s.err
}

type failingPut struct{ storage.StorageBackend }

func (failingPut) Put(context.Context, string, io.Reader) error {
	return errors.New("bucket unavailable")
}

func patchedStats() map[string]string {
	return map[string]string{"/sbin": statDir, "/sbin/init": statInit}
}

func fakePatchInput(t *testing.T, body []byte) (BasePatchInput, storage.StorageBackend) {
	t.Helper()
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	source := filepath.Join(t.TempDir(), "base.ext4")
	if err := os.WriteFile(source, []byte("ext4 image"), 0o644); err != nil {
		t.Fatal(err)
	}
	gi, sum := writeGuestInit(t, body)
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return BasePatchInput{
		SourceImage: source, GuestInitPath: gi, GuestInitSHA256: sum,
		Storage: be, StorageKey: "base/runner-test-amd64.ext4",
	}, be
}

func TestPatchBaseGuestInitPublishesAndSignsTheVerifiedCopy(t *testing.T) {
	in, be := fakePatchInput(t, []byte("new"))
	run := &fakeDebugfs{stats: patchedStats(), dump: []byte("new")}
	signer := &recordingSigner{}
	res, err := NewBuilder(run).WithSigner(signer).PatchBaseGuestInit(context.Background(), in)
	if err != nil {
		t.Fatalf("PatchBaseGuestInit: %v", err)
	}
	if res.ImageKey != in.StorageKey || res.SizeBytes != int64(len("ext4 image")) {
		t.Fatalf("result = %+v", res)
	}
	if got := readStored(t, be, in.StorageKey); !bytes.Equal(got, []byte("ext4 image")) {
		t.Fatalf("published %q", got)
	}
	identity, err := ReadArtifactIdentity(t.Context(), bytes.NewReader(readStored(t, be, in.StorageKey)))
	if err != nil {
		t.Fatal(err)
	}
	if res.ArtifactDigest != identity.Digest || res.ArtifactBytes != identity.Bytes || res.GuestInitDigest != "sha256:"+in.GuestInitSHA256 {
		t.Fatalf("result identity = %+v, published identity = %+v", res, identity)
	}
	if len(signer.keys) != 1 || signer.keys[0] != in.StorageKey+"->sigs/"+in.StorageKey+".sig" {
		t.Fatalf("signed %v", signer.keys)
	}
	var script string
	for _, c := range run.calls {
		if strings.HasPrefix(c, "env E2FSPROGS_FAKE_TIME="+basePatchFakeTime+" debugfs -w -f ") {
			script = c
		}
	}
	if script == "" {
		t.Fatalf("no pinned-time debugfs write in %v", run.calls)
	}
}

func TestPatchBaseGuestInitFailuresPublishNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		run    *fakeDebugfs
		mutate func(in *BasePatchInput)
		signer Signer
		want   string
	}{
		"source image missing": {run: &fakeDebugfs{stats: patchedStats()}, mutate: func(in *BasePatchInput) {
			in.SourceImage = filepath.Join(filepath.Dir(in.SourceImage), "gone.ext4")
		}, want: "open source base"},
		"debugfs write fails": {run: &fakeDebugfs{stats: patchedStats(), writeErr: errors.New("boom")}, want: "replace guest-init"},
		"dump fails":          {run: &fakeDebugfs{stats: patchedStats(), dumpErr: errors.New("boom")}, want: "dump patched init"},
		"dump differs":        {run: &fakeDebugfs{stats: patchedStats(), dump: []byte("old")}, want: "patched init sha256"},
		"e2fsck fails":        {run: &fakeDebugfs{stats: patchedStats(), dump: []byte("new"), fsckErr: errors.New("4")}, want: "e2fsck"},
		"signing fails": {run: &fakeDebugfs{stats: patchedStats(), dump: []byte("new")},
			signer: &recordingSigner{err: errors.New("no key")}, want: "sign base"},
		"publish fails": {run: &fakeDebugfs{stats: patchedStats(), dump: []byte("new")}, mutate: func(in *BasePatchInput) {
			in.Storage = failingPut{in.Storage}
		}, want: "publish base"},
	} {
		t.Run(name, func(t *testing.T) {
			in, be := fakePatchInput(t, []byte("new"))
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			b := NewBuilder(tc.run)
			if tc.signer != nil {
				b.WithSigner(tc.signer)
			}
			_, err := b.PatchBaseGuestInit(context.Background(), in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if name != "signing fails" {
				if _, getErr := be.Get(context.Background(), in.StorageKey); getErr == nil {
					t.Fatal("a failed patch published an image")
				}
			}
		})
	}
}

func TestBaseInitDirRefusesUnexpectedLayouts(t *testing.T) {
	for name, tc := range map[string]struct {
		run         *fakeDebugfs
		unsupported bool
	}{
		"stat fails":            {run: &fakeDebugfs{statErr: errors.New("boom")}},
		"sbin missing":          {run: &fakeDebugfs{stats: map[string]string{}}, unsupported: true},
		"sbin is a file":        {run: &fakeDebugfs{stats: map[string]string{"/sbin": statInit}}, unsupported: true},
		"slow symlink":          {run: &fakeDebugfs{stats: map[string]string{"/sbin": strings.Replace(statSymlink, "Fast link dest: \"usr/sbin\"\n", "", 1)}}, unsupported: true},
		"symlink to a file":     {run: &fakeDebugfs{stats: map[string]string{"/sbin": statSymlink, "/usr/sbin": statInit}}, unsupported: true},
		"symlink target unsafe": {run: &fakeDebugfs{stats: map[string]string{"/sbin": strings.Replace(statSymlink, "usr/sbin", "usr/s bin", 1), "/usr/s bin": statDir}}, unsupported: true},
		"init is a symlink":     {run: &fakeDebugfs{stats: map[string]string{"/sbin": statDir, "/sbin/init": statSymlink}}, unsupported: true},
		"init owner unreadable": {run: &fakeDebugfs{stats: map[string]string{"/sbin": statDir, "/sbin/init": "Inode: 12   Type: regular    Mode:  0755\n"}}, unsupported: true},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := baseInitDir(context.Background(), tc.run, "/srv/fc/base/a.ext4")
			if err == nil {
				t.Fatal("baseInitDir accepted the layout")
			}
			if tc.unsupported != errors.Is(err, ErrBasePatchUnsupported) {
				t.Fatalf("err = %v, unsupported = %v", err, tc.unsupported)
			}
		})
	}

	dir, current, err := baseInitDir(context.Background(), &fakeDebugfs{stats: map[string]string{
		"/sbin": statSymlink, "/usr/sbin": statDir, "/usr/sbin/init": statInit,
	}}, "/srv/fc/base/a.ext4")
	if err != nil || dir != "/usr/sbin" || current.User != "992" || current.Group != "988" {
		t.Fatalf("usrmerged layout = (%q, %+v, %v)", dir, current, err)
	}
}

func TestVerifyBaseInitRejectsWrongMetadata(t *testing.T) {
	current := debugfsInode{Type: "regular", Mode: "0755", User: "992", Group: "988"}
	for name, stat := range map[string]string{
		"mode 0644":   strings.Replace(statInit, "0755", "0644", 1),
		"root owned":  strings.Replace(statInit, "User:   992   Group:   988", "User:     0   Group:     0", 1),
		"not regular": statSymlink,
	} {
		t.Run(name, func(t *testing.T) {
			run := &fakeDebugfs{stats: map[string]string{"/sbin/init": stat}, dump: []byte("new")}
			if err := verifyBaseInit(context.Background(), run, filepath.Join(t.TempDir(), "a.ext4"), "/sbin/init", current, strings.Repeat("0", 64)); err == nil {
				t.Fatal("verifyBaseInit accepted it")
			}
		})
	}
}

func TestCreateBaseTempReportsAnUnusableRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_BASE_TMP_ROOT", filepath.Join(file, "sub"))
	if _, err := createBaseTemp("x-*.ext4"); err == nil {
		t.Fatal("createBaseTemp succeeded under a regular file")
	}
}
