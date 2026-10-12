package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryDirectoryOpenPreservesGuardedBoundary(t *testing.T) {
	root := t.TempDir()
	directory, err := openCustomerDirectoryForSync(root)
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
	if f, err := openCustomerDirectoryForSync(regular); err == nil {
		_ = f.Close()
		t.Fatal("accepted regular file as directory")
	}
	link := filepath.Join(root, "symlink")
	if err = os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if f, err := openCustomerDirectoryForSync(link); err == nil {
		_ = f.Close()
		t.Fatal("followed directory symlink")
	}
	if f, err := openCustomerFile(link); err == nil {
		_ = f.Close()
		t.Fatal("followed customer file symlink")
	}
}
