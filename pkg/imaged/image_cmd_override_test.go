// adr: 053 — a cmd override replaces the image CMD and keeps its ENTRYPOINT.
package imaged

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestImageCmdOverrideReplacesImageCmd(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  oci.ImageConfig
		dep  state.Deployment
		want []string
	}{
		{
			name: "cmd-only image (busybox) runs the override alone",
			cfg:  oci.ImageConfig{Cmd: []string{"sh"}},
			dep:  state.Deployment{OverrideCmd: []string{"sh", "-c", "echo tick"}},
			want: []string{"sh", "-c", "echo tick"},
		},
		{
			name: "entrypoint image keeps its entrypoint",
			cfg:  oci.ImageConfig{Entrypoint: []string{"/docker-entrypoint.sh"}, Cmd: []string{"nginx", "-g", "daemon off;"}},
			dep:  state.Deployment{OverrideCmd: []string{"nginx", "-T"}},
			want: []string{"/docker-entrypoint.sh", "nginx", "-T"},
		},
		{
			name: "explicit entrypoint override keeps its precedence",
			cfg:  oci.ImageConfig{Entrypoint: []string{"/docker-entrypoint.sh"}, Cmd: []string{"nginx"}},
			dep:  state.Deployment{OverrideEntrypoint: []string{"/bin/custom"}, OverrideCmd: []string{"--flag"}},
			want: []string{"/bin/custom", "--flag"},
		},
		{
			name: "no override keeps the image argv",
			cfg:  oci.ImageConfig{Entrypoint: []string{"/docker-entrypoint.sh"}, Cmd: []string{"nginx"}},
			want: []string{"/docker-entrypoint.sh", "nginx"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := manifestFromImageConfig(tc.cfg)
			if err != nil {
				t.Fatalf("manifestFromImageConfig: %v", err)
			}
			manifest, err = applyOverrides(imageEntrypointForCmdOverride(manifest, tc.cfg, tc.dep), tc.dep)
			if err != nil {
				t.Fatalf("applyOverrides: %v", err)
			}
			if !reflect.DeepEqual(manifest.Entrypoint, tc.want) {
				t.Fatalf("argv = %q, want %q", manifest.Entrypoint, tc.want)
			}
		})
	}
}
