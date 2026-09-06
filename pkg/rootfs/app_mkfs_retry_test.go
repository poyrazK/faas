package rootfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

type appMkfsRetryRunner struct {
	failures int
	failure  error
	sizes    []int
}

func (r *appMkfsRetryRunner) Run(_ context.Context, argv []string) error {
	size, err := strconv.Atoi(strings.TrimSuffix(argv[len(argv)-1], "M"))
	if err != nil {
		return err
	}
	r.sizes = append(r.sizes, size)
	if r.failures > 0 {
		r.failures--
		return r.failure
	}
	file, err := os.Create(argv[len(argv)-2])
	if err != nil {
		return err
	}
	if err := file.Truncate(int64(size) * mib); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

type appMkfsSigner struct{ calls int }

func (s *appMkfsSigner) Sign(context.Context, string, string) error { s.calls++; return nil }

func TestAppMkfsRetriesBeforePublicationAndReportsFinalSize(t *testing.T) {
	run := &appMkfsRetryRunner{failures: 1, failure: errors.New("mkfs.ext4: Could not allocate block in ext2 filesystem while populating file system")}
	storeRoot := t.TempDir()
	store, err := storage.NewLocalStorageBackend(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	guest := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guest, []byte("guest"), 0755); err != nil {
		t.Fatal(err)
	}
	signer := &appMkfsSigner{}
	b := NewBuilder(run).WithSigner(signer)
	result, err := b.Build(context.Background(), BuildInput{Manifest: api.AppManifest{Entrypoint: []string{"/app/handler"}}, GuestInitPath: guest, Plan: api.PlanFree, Storage: store, StorageKey: "app/layer.ext4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.sizes) != 2 || run.sizes[0] != 16 || run.sizes[1] != 20 {
		t.Fatalf("mkfs attempts=%v", run.sizes)
	}
	if result.SizeMB != 20 || signer.calls != 1 {
		t.Fatalf("result size=%d signatures=%d", result.SizeMB, signer.calls)
	}
	info, err := os.Stat(filepath.Join(storeRoot, "app/layer.ext4"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(result.SizeMB)*mib {
		t.Fatalf("published bytes=%d result=%dMiB", info.Size(), result.SizeMB)
	}
}

func TestAppMkfsRetryBudgetAndCap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cap      int
		failure  error
		attempts int
		wantCap  bool
	}{
		{"cap", 20, errors.New("Could not allocate block"), 2, true},
		{"last partial growth to cap", 18, errors.New("Could not allocate inode"), 2, true},
		{"attempt budget", 256, errors.New("Could not allocate block"), appMkfsMaxAttempts, false},
		{"host disk full", 256, errors.New("write image: no space left on device"), 1, false},
		{"permission", 256, os.ErrPermission, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &appMkfsRetryRunner{failures: 10, failure: tc.failure}
			limits, _ := api.LimitsFor(api.PlanFree)
			limits.AppLayerMaxMB = tc.cap
			_, err := NewBuilder(run).runAppMkfs(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "image"), 16, limits)
			if err == nil || len(run.sizes) != tc.attempts {
				t.Fatalf("attempts=%v err=%v", run.sizes, err)
			}
			for _, size := range run.sizes {
				if size > tc.cap {
					t.Fatalf("attempted %d over cap %d", size, tc.cap)
				}
			}
			var problem *api.Problem
			if errors.As(err, &problem) != tc.wantCap {
				t.Fatalf("cap classification err=%v want=%v", err, tc.wantCap)
			}
		})
	}
}

func TestAppMkfsCancellationDoesNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &appMkfsRetryRunner{}
	limits, _ := api.LimitsFor(api.PlanFree)
	_, err := NewBuilder(run).runAppMkfs(ctx, t.TempDir(), filepath.Join(t.TempDir(), "image"), 16, limits)
	if !errors.Is(err, context.Canceled) || len(run.sizes) != 0 {
		t.Fatalf("err=%v attempts=%v", err, run.sizes)
	}
}

func TestAppMkfsFailureDoesNotPublishOrSign(t *testing.T) {
	run := &appMkfsRetryRunner{failures: 10, failure: errors.New("Could not allocate block")}
	root := t.TempDir()
	store, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	guest := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guest, []byte("guest"), 0755); err != nil {
		t.Fatal(err)
	}
	signer := &appMkfsSigner{}
	_, err = NewBuilder(run).WithSigner(signer).Build(context.Background(), BuildInput{Manifest: api.AppManifest{Entrypoint: []string{"/app/handler"}}, GuestInitPath: guest, Plan: api.PlanFree, Storage: store, StorageKey: "app/layer.ext4"})
	if err == nil || signer.calls != 0 {
		t.Fatalf("err=%v signatures=%d", err, signer.calls)
	}
	if _, err := os.Stat(filepath.Join(root, "app/layer.ext4")); !os.IsNotExist(err) {
		t.Fatalf("published failed image: %v", err)
	}
}
