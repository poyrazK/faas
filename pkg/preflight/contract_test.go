package preflight

import (
	"testing"
	"testing/fstest"
)

// A VOLUME declaration says the app expects durable local disk. The Gregale
// root filesystem is ephemeral, so this is a hard disqualifier and the check
// must say no rather than let someone deploy and lose data.
func TestScanContract_DockerfileVolumeIsRed(t *testing.T) {
	fsys := fstest.MapFS{
		"Dockerfile": &fstest.MapFile{Data: []byte("FROM node:22\nVOLUME /data\nCMD [\"node\", \"server.js\"]\n")},
	}

	findings := ScanContract(fsys)

	var found *Finding
	for i := range findings {
		if findings[i].Code == "durable_local_disk" {
			found = &findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no durable_local_disk finding, got %+v", findings)
	}
	if found.Level != LevelRed {
		t.Errorf("Level = %q, want %q", found.Level, LevelRed)
	}
	if len(found.Sources) == 0 || found.Sources[0] != "Dockerfile" {
		t.Errorf("Sources = %v, want [Dockerfile]", found.Sources)
	}
}

// A stateless image with no disqualifiers must produce nothing. The scanner
// exists to find hard stops, not to editorialize.
func TestScanContract_CleanDockerfileHasNoFindings(t *testing.T) {
	fsys := fstest.MapFS{
		"Dockerfile": &fstest.MapFile{Data: []byte("FROM node:22\nEXPOSE 3000\nCMD [\"node\", \"server.js\"]\n")},
	}

	if findings := ScanContract(fsys); len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}

// Every hard disqualifier in docs/container-compatibility.md must be detected
// from source alone. Each case names the file and the exact construct that
// makes the app undeployable.
func TestScanContract_HardDisqualifiers(t *testing.T) {
	cases := []struct {
		name string
		fsys fstest.MapFS
		code string
	}{
		{
			name: "arm64 platform pin",
			fsys: fstest.MapFS{"Dockerfile": &fstest.MapFile{
				Data: []byte("FROM --platform=linux/arm64 node:22\nCMD [\"node\", \"s.js\"]\n")}},
			code: "unsupported_architecture",
		},
		{
			name: "privileged compose service",
			fsys: fstest.MapFS{"docker-compose.yml": &fstest.MapFile{
				Data: []byte("services:\n  api:\n    image: x\n    privileged: true\n")}},
			code: "privileged_mode",
		},
		{
			name: "host networking",
			fsys: fstest.MapFS{"docker-compose.yml": &fstest.MapFile{
				Data: []byte("services:\n  api:\n    image: x\n    network_mode: host\n")}},
			code: "host_networking",
		},
		{
			name: "docker socket mount",
			fsys: fstest.MapFS{"docker-compose.yaml": &fstest.MapFile{
				Data: []byte("services:\n  api:\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n")}},
			code: "docker_socket",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := ScanContract(tc.fsys)
			for _, f := range findings {
				if f.Code == tc.code {
					if f.Level != LevelRed {
						t.Fatalf("%s Level = %q, want %q", tc.code, f.Level, LevelRed)
					}
					if f.Remedy == "" {
						t.Errorf("%s has no remedy", tc.code)
					}
					return
				}
			}
			t.Fatalf("no %s finding, got %+v", tc.code, findings)
		})
	}
}
