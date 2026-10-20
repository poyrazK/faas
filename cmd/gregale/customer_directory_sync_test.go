package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryDirectoryOpenPreservesGuardedBoundary(t *testing.T) {
	root := t.TempDir()
	directory, err := main.OpenCustomerDirectoryForSync(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		t.Fatalf("directory descriptor: %v %v", info, err)
	}
	_ = directory.Close()
	regular := filepath.Join(root, "plan.json")
	if err = os.WriteFile(regular, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := main.OpenCustomerDirectoryForSync(regular); err == nil {
		_ = f.Close()
		t.Fatal("accepted regular file as directory")
	}
	link := filepath.Join(root, "symlink")
	if err = os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if f, err := main.OpenCustomerDirectoryForSync(link); err == nil {
		_ = f.Close()
		t.Fatal("followed directory symlink")
	}
	if f, err := main.OpenCustomerFile(link); err == nil {
		_ = f.Close()
		t.Fatal("followed customer file symlink")
	}
}
