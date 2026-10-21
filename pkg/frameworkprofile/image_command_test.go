// adr: 1337
package frameworkprofile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCaptureImageRuntimeVersionsTheProcessContract(t *testing.T) {
	cleared := []string{}
	grace := 20
	process := &api.ComposeProcess{Entrypoint: &cleared, User: "app", WorkingDir: "/srv", StopSignal: "SIGINT", StopGracePeriodS: &grace}
	for _, tc := range []struct {
		name    string
		process *api.ComposeProcess
		version string
	}{
		{"no process stays v1", nil, ImageCommandVersion},
		{"empty process stays v1", &api.ComposeProcess{}, ImageCommandVersion},
		{"process is v2", process, ImageCommandProcessVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := CaptureImageRuntime([]string{"serve"}, nil, tc.process)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ImageCommandFromProfile(profile)
			if err != nil || got == nil {
				t.Fatalf("decode = %+v, %v", got, err)
			}
			want := tc.process
			if want.IsZero() {
				want = nil
			}
			if got.Version != tc.version || !reflect.DeepEqual(got.Cmd, []string{"serve"}) || !reflect.DeepEqual(got.Process, want) {
				t.Fatalf("decoded %+v; want version %s process %+v", got, tc.version, want)
			}
		})
	}
}

func TestCaptureImageRuntimeRejectsInvalidProcess(t *testing.T) {
	if _, err := CaptureImageRuntime(nil, nil, &api.ComposeProcess{WorkingDir: "relative"}); err == nil {
		t.Fatal("invalid process captured")
	}
}

func TestImageCommandFromProfileRejectsMismatchedContracts(t *testing.T) {
	for name, raw := range map[string]string{
		"v1 carrying a process": `{"image_command":{"version":"v1","cmd":null,"process":{"user":"app"}}}`,
		"v2 without a process":  `{"image_command":{"version":"v2","cmd":null}}`,
		"v2 with empty process": `{"image_command":{"version":"v2","cmd":null,"process":{}}}`,
		"unknown version":       `{"image_command":{"version":"v3","cmd":null,"process":{"user":"app"}}}`,
		"invalid process":       `{"image_command":{"version":"v2","cmd":null,"process":{"working_dir":"rel"}}}`,
		"missing cmd":           `{"image_command":{"version":"v2","process":{"user":"app"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ImageCommandFromProfile([]byte(raw)); err == nil {
				t.Fatalf("accepted %s as %+v", raw, got)
			}
		})
	}
	got, err := ImageCommandFromProfile([]byte(`{"image_command":{"version":"v1","cmd":null}}`))
	if err != nil || got == nil || got.Process != nil || got.Cmd != nil {
		t.Fatalf("legacy v1 inherit = %+v, %v", got, err)
	}
}

func TestPreserveImageRuntimeKeepsTheProcessContract(t *testing.T) {
	previous, err := CaptureImageRuntime(nil, nil, &api.ComposeProcess{User: "app"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := PreserveImageRuntime(previous, []byte(`{"version":"1","framework":"node","image_command":{"version":"v1","cmd":["evil"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ImageCommandFromProfile(updated)
	if err != nil || got == nil || got.Process == nil || got.Process.User != "app" || got.Cmd != nil {
		t.Fatalf("preserved = %+v, %v; want the admission-time v2 contract", got, err)
	}
	if !strings.Contains(string(updated), `"framework":"node"`) {
		t.Fatalf("worker metadata lost: %s", updated)
	}
}
