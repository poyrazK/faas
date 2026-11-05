package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckBridgeUIDRange(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	clean := write("passwd", "root:x:0:0:root:/root:/bin/bash\nfaas:x:993:993::/:/usr/sbin/nologin\n")
	cleanGroup := write("group", "root:x:0:\nfaas:x:993:\n")
	userInRange := write("passwd-user", "root:x:0:0::/root:/bin/bash\nci:x:30001:1000::/home/ci:/bin/bash\n")
	gidInRange := write("passwd-gid", "ci:x:1000:39999::/home/ci:/bin/bash\n")
	groupInRange := write("group-range", "docker:x:35000:\n")
	on := func(string) string { return "1" }

	tests := []struct {
		name          string
		getenv        func(string) string
		passwd, group string
		wantErr       string
	}{
		{name: "switch off ignores accounts", getenv: func(string) string { return "" }, passwd: userInRange, group: groupInRange},
		{name: "clean host", getenv: on, passwd: clean, group: cleanGroup},
		{name: "missing files", getenv: on, passwd: filepath.Join(dir, "none"), group: filepath.Join(dir, "none")},
		{name: "user uid in range", getenv: on, passwd: userInRange, group: cleanGroup, wantErr: "ci ("},
		{name: "user gid in range", getenv: on, passwd: gidInRange, group: cleanGroup, wantErr: "39999"},
		{name: "group in range", getenv: on, passwd: clean, group: groupInRange, wantErr: "docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBridgeUIDRange(tt.getenv, tt.passwd, tt.group)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("checkBridgeUIDRange: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("checkBridgeUIDRange error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAdoptionConfig(t *testing.T) {
	for _, tt := range []struct {
		record, adopt, native, ok bool
	}{
		{ok: true},
		{record: true, ok: true},
		{record: true, adopt: true, ok: true},
		{adopt: true},
		{record: true, adopt: true, native: true},
		{record: true, native: true, ok: true},
	} {
		if err := validateAdoptionConfig(tt.record, tt.adopt, tt.native); (err == nil) != tt.ok {
			t.Errorf("validateAdoptionConfig(record=%v, adopt=%v, native=%v) = %v", tt.record, tt.adopt, tt.native, err)
		}
	}
}

type slowStopper struct {
	release chan struct{}
	stopped bool
}

func (s *slowStopper) GracefulStop() { <-s.release }
func (s *slowStopper) Stop()         { s.stopped = true; close(s.release) }

func TestStopGRPCBoundsAnAdoptingStop(t *testing.T) {
	s := &slowStopper{release: make(chan struct{})}
	started := time.Now()
	stopGRPC(s, true, slog.Default())
	if !s.stopped || time.Since(started) > adoptingStopGrace+time.Second {
		t.Fatalf("adopting stop: forced=%v after %s", s.stopped, time.Since(started))
	}
	done := make(chan struct{})
	quick := &slowStopper{release: make(chan struct{})}
	go func() { stopGRPC(quick, false, slog.Default()); close(done) }()
	select {
	case <-done:
		t.Fatal("a non-adopting stop did not wait for open RPCs")
	case <-time.After(3 * time.Second):
	}
	close(quick.release)
	<-done
}
