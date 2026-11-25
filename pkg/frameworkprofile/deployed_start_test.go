package frameworkprofile

import (
	"testing"
	"testing/fstest"
)

// Prod hunt r47: the deploy summary showed Go's local `go run .` as the
// deployed start command; the build runs the compiled ./out binary.
func TestDeployedStartCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile Profile
		want    string
	}{
		{"inferred go", Profile{StartCommand: "go run .", GoBuiltStart: true}, "./out"},
		{"go with a configured start", Profile{StartCommand: "go run .", StartFromConfig: true}, "go run ."},
		{"profile written before the marker", Profile{StartCommand: "go run ."}, "go run ."},
		{"node", Profile{StartCommand: "npm run start"}, "npm run start"},
		{"none", Profile{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeployedStartCommand(tc.profile); got != tc.want {
				t.Fatalf("DeployedStartCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// A Procfile or railpack.json `go run .` is what the guest runs, so the
// deployed start command keeps it.
func TestDeployedStartCommandKeepsAnExplicitGoRun(t *testing.T) {
	goMod := &fstest.MapFile{Data: []byte("module example.com/app\n\ngo 1.22\n")}
	main := &fstest.MapFile{Data: []byte("package main\n\nfunc main() {}\n")}
	for _, tc := range []struct {
		name string
		file string
		data string
		want string
	}{
		{"inferred", "", "", "./out"},
		{"procfile", "Procfile", "web: go run .\n", "go run ."},
		{"railpack", "railpack.json", `{"deploy":{"startCommand":"go run ."}}`, "go run ."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"go.mod": goMod, "main.go": main}
			if tc.file != "" {
				fsys[tc.file] = &fstest.MapFile{Data: []byte(tc.data)}
			}
			p, err := Analyze(fsys)
			if err != nil {
				t.Fatal(err)
			}
			if got := DeployedStartCommand(p); got != tc.want {
				t.Fatalf("DeployedStartCommand = %q (start %q), want %q", got, p.StartCommand, tc.want)
			}
		})
	}
}
