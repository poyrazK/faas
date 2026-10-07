package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 639
func TestBucketUploadCheckpointPrivateAtomicAndLocked(t *testing.T) {
	c, o := newBucketResumeFixture(t)
	c.failSign = 2
	result, err := runBucketTransfer(t.Context(), c, o)
	if err == nil {
		t.Fatal("fixture did not interrupt")
	}
	path, err := bucketUploadCheckpointPath(c.BaseURL(), result.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("checkpoint is not private", info, err)
	}
	cp, err := loadBucketUploadCheckpoint(path)
	if err != nil || cp.Phase != "uploading" || cp.Parts[0].ETag == "" || !cp.Parts[1].Attempted {
		t.Fatal(cp, err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), c.url) || strings.Contains(string(data), "Bearer") {
		t.Fatal("capability credentials were persisted", err)
	}
	lock, err := lockBucketUploadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	o.resumeID = result.UploadID
	if _, err := runBucketTransfer(t.Context(), c, o); err == nil || c.reads != 0 {
		t.Fatal("concurrent resume bypassed checkpoint lock", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	// A killed CLI cannot run its staging cleanup defer. Resume must discard
	// only its own private stale part after acquiring the session lock.
	if err := os.WriteFile(path+".part", []byte("stale staging bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runBucketTransfer(t.Context(), c, o); err != nil {
		t.Fatal("resume failed after releasing the lock", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 2 {
		t.Fatal("temporary checkpoint files leaked", entries, err)
	}
	other, err := bucketUploadCheckpointPath("https://other.example.test", result.UploadID)
	if err != nil || other == path {
		t.Fatal("API endpoint was not bound to checkpoint", other, err)
	}
}

// adr: 639
func TestBucketUploadResumeRefusesSymlinkedStage(t *testing.T) {
	c, o := newBucketResumeFixture(t)
	c.failSign = 2
	result, err := runBucketTransfer(t.Context(), c, o)
	if err == nil {
		t.Fatal("fixture did not interrupt")
	}
	path, err := bucketUploadCheckpointPath(c.BaseURL(), result.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(o.path, path+".part"); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	o.resumeID = result.UploadID
	if _, err := runBucketTransfer(t.Context(), c, o); err == nil || c.reads != 0 {
		t.Fatal("symlinked stage was accepted", err)
	}
	if data, err := os.ReadFile(o.path); err != nil || string(data) != "abcdefg" {
		t.Fatal("stage cleanup affected another file", err)
	}
}

// adr: 639
func TestBucketUploadCheckpointRejectsCorruptionAndSymlinks(t *testing.T) {
	for _, scenario := range []string{"trailing", "unknown field", "version", "fingerprint", "parts", "phase", "oversized", "symlink", "public permissions"} {
		t.Run(scenario, func(t *testing.T) {
			c, o := newBucketResumeFixture(t)
			c.failSign = 2
			result, err := runBucketTransfer(t.Context(), c, o)
			if err == nil {
				t.Fatal("fixture did not interrupt")
			}
			path, err := bucketUploadCheckpointPath(c.BaseURL(), result.UploadID)
			if err != nil {
				t.Fatal(err)
			}
			cp, err := loadBucketUploadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "version":
				cp.Version++
			case "fingerprint":
				cp.SHA256 = "not-a-digest"
			case "parts":
				cp.Parts = cp.Parts[:1]
			case "phase":
				cp.Phase = "completing"
			}
			if err := saveBucketUploadCheckpoint(path, cp); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "trailing":
				data = append(data, []byte(`{}`)...)
			case "unknown field":
				data = []byte(strings.Replace(string(data), `"version":1`, `"unexpected":true,"version":1`, 1))
			case "oversized":
				data = make([]byte, api.MaxObjectMultipartCheckpointBytes+1)
			case "symlink":
				if err := os.Rename(path, path+".private"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".private", path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "public permissions":
				if runtime.GOOS == "windows" {
					t.Skip("Windows privacy relies on the user configuration directory ACL")
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "symlink" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			o.resumeID = result.UploadID
			if _, err := runBucketTransfer(t.Context(), c, o); err == nil || c.reads != 0 {
				t.Fatal("invalid checkpoint contacted API", err)
			}
		})
	}
}
