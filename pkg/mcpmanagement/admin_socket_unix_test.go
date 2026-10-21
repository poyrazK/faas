//go:build unix

package mcpmanagement

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func privateAdminDirectory(t *testing.T) string {
	t.Helper()
	path, err := os.MkdirTemp("", "gmcp-admin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}
func TestAdminSocketPermissionsClientAndCleanup(t *testing.T) {
	parent := privateAdminDirectory(t)
	path := filepath.Join(parent, "control.sock")
	listener, err := ListenAdminSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"items":[]}`)) })}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket is not owner-only")
	}
	client, err := AdminClient(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://unix/connections")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("socket control request failed")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("socket survived close")
	}
}
func TestAdminSocketRefusesUnsafeAndExistingPaths(t *testing.T) {
	parent := privateAdminDirectory(t)
	path := filepath.Join(parent, "control.sock")
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenAdminSocket(path); err == nil {
		t.Fatal("publicly accessible parent accepted")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenAdminSocket("relative.sock"); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenAdminSocket(path); err == nil {
		t.Fatal("existing file overwritten")
	}
	if _, err := AdminClient(path); err == nil {
		t.Fatal("regular file accepted as socket")
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "keep" {
		t.Fatal("existing path changed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := ListenAdminSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	if _, err := AdminClient(path); err == nil {
		t.Fatal("group-accessible socket accepted")
	}
	link := filepath.Join(parent, "link.sock")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := AdminClient(link); err == nil {
		t.Fatal("symlink accepted as socket")
	}
}
