package fcvm

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261110210000 — slice 3j-3: with a build registry mirror, a builder
// gets the mirror plan and no password; vmmd opens nothing, even with no
// build identity at all. A request without a build id (an older builderd)
// keeps the Docker config.
func TestBuilderWithMirrorGetsNoPassword(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	asked := map[string]string{}
	m.SetBuildRegistryMirror(func(buildID, registry string) http.Handler {
		asked[registry] = buildID
		return http.NotFoundHandler()
	})
	creds := []BuildRegistryCredential{
		{Registry: "registry.example.com", Username: "u", PasswordSealed: []byte("not-openable")},
		{Registry: "ghcr.io", Username: "octo", PasswordSealed: []byte("not-openable")},
	}
	req := WakeRequest{ExportDir: "/export", BuildID: "build-1", BuildRegistryCredentials: creds}

	files, _, err := m.prepareWakeFiles(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	env := secretsEnvOf(t, files)
	if _, ok := env[api.BuildRegistryAuthKey]; ok {
		t.Fatal("a builder with a mirror got a Docker config")
	}
	plan, err := api.ParseBuildRegistryMirrors(env[api.BuildRegistryMirrorsKey])
	if err != nil {
		t.Fatalf("mirror plan: %v (%q)", err, env[api.BuildRegistryMirrorsKey])
	}
	if len(plan) != 2 || plan[0].Registry != "ghcr.io" || plan[1].Registry != "registry.example.com" {
		t.Fatalf("plan = %+v", plan)
	}

	mirrors := m.leaseBuildMirrors(req).list()
	if len(mirrors) != 2 || mirrors[0].port != plan[0].VsockPort || mirrors[1].port != plan[1].VsockPort {
		t.Fatalf("lease mirrors = %+v", mirrors)
	}
	if asked["ghcr.io"] != "build-1" || asked["registry.example.com"] != "build-1" {
		t.Fatalf("mirror handlers asked for %v", asked)
	}

	// No build id: the older path, which here opens nothing (no identity).
	files, _, err = m.prepareWakeFiles(context.Background(), WakeRequest{ExportDir: "/export", BuildRegistryCredentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := secretsEnvOf(t, files)[api.BuildRegistryMirrorsKey]; ok {
		t.Fatal("a builder without a build id got a mirror plan")
	}
	if m.leaseBuildMirrors(WakeRequest{ExportDir: "/export", BuildRegistryCredentials: creds}) != nil {
		t.Fatal("a builder without a build id got lease mirrors")
	}
	// An app VM never gets mirrors.
	if m.leaseBuildMirrors(WakeRequest{BuildID: "build-1", BuildRegistryCredentials: creds}) != nil {
		t.Fatal("a VM that is not a builder got lease mirrors")
	}
}
