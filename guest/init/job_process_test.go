package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// adr: 1790
func TestBuildImageJobEnvLayersLikeDocker(t *testing.T) {
	base := []string{"HOME=/", "TERM=linux", "SHARED=base"}
	image := map[string]string{"PATH": "/opt/app/bin:/usr/bin", "SHARED": "image", "IMAGE_ONLY": "1", "FAAS_JOB": "spoofed"}
	m := JobManifest{Env: map[string]string{"SHARED": "run", "RUN_ONLY": "2"}}
	got := envMap(buildImageJobEnv(base, image, m, map[string]string{"SECRET": "s"}))
	want := map[string]string{
		"HOME": "/", "TERM": "linux", "PATH": "/opt/app/bin:/usr/bin", "SHARED": "run", "IMAGE_ONLY": "1",
		"RUN_ONLY": "2", "SECRET": "s", "FAAS_JOB": "1", "FAAS_RUNTIME_KIND": "job",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %v\nwant %v", got, want)
	}
	if got := envMap(buildImageJobEnv(nil, nil, JobManifest{}, nil)); got["PATH"] != defaultImagePath {
		t.Fatalf("PATH without an image PATH = %q, want Docker's default", got["PATH"])
	}
}

// adr: 1790
func TestImageJobUserAndWorkdirPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		m             JobManifest
		app           api.AppManifest
		user, workdir string
	}{
		{name: "image without USER runs as root in /", user: "0", workdir: "/"},
		{name: "image USER and WORKDIR", app: api.AppManifest{User: "node", WorkingDir: "/srv"}, user: "node", workdir: "/srv"},
		{name: "overrides win", m: JobManifest{User: "1000:0", Workdir: "/tmp/run"},
			app: api.AppManifest{User: "node", WorkingDir: "/srv"}, user: "1000:0", workdir: "/tmp/run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageJobUser(tc.m, tc.app); got != tc.user {
				t.Fatalf("user = %q, want %q", got, tc.user)
			}
			if got := imageJobWorkdir(tc.m, tc.app); got != tc.workdir {
				t.Fatalf("workdir = %q, want %q", got, tc.workdir)
			}
		})
	}
}

// adr: 1790
func TestPasswdHomeAndImageHome(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/sh\n# comment:x:5:5::/nope:/bin/sh\nnode:x:1000:1000::/home/node:/bin/sh\nbad:x:7:7::relative:/bin/sh\n"
	for _, tc := range []struct {
		uid  uint32
		want string
	}{{0, "/root"}, {1000, "/home/node"}, {7, "/"}, {4242, "/"}} {
		if got := passwdHome(passwd, tc.uid); got != tc.want {
			t.Errorf("passwdHome(%d) = %q, want %q", tc.uid, got, tc.want)
		}
	}
	if got := envMap(withImageHome([]string{"PATH=/bin"}, "/root")); got["HOME"] != "/root" {
		t.Fatalf("unset HOME = %q, want /root", got["HOME"])
	}
	if got := envMap(withImageHome([]string{"HOME=/data"}, "/root")); got["HOME"] != "/data" {
		t.Fatalf("image HOME was replaced: %q", got["HOME"])
	}
	if got := withImageHome([]string{"HOME="}, "/root"); len(got) != 1 || got[0] != "HOME=/root" {
		t.Fatalf("empty HOME = %q, want one HOME=/root", got)
	}
}

func TestGuestJobProcessKeepsOrdinaryJobs(t *testing.T) {
	m := JobManifest{Command: []string{"/bin/true", "x"}}
	proc := guestJobProcess(m, []string{"A=1"})
	if !reflect.DeepEqual(proc.Argv, m.Command) || proc.Dir != "" || proc.Credential != nil || !reflect.DeepEqual(proc.Env, []string{"A=1"}) {
		t.Fatalf("ordinary process = %+v", proc)
	}
	proc.Argv[0] = "changed"
	if m.Command[0] != "/bin/true" {
		t.Fatal("process argv aliases the manifest command")
	}
}
