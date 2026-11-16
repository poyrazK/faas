package frameworkprofile_test

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

func TestDevServerHMRRecognition(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		deps               map[string]string
	}{
		{name: "next", script: "next dev --turbo", deps: map[string]string{"next": "16"}, want: "Next.js"},
		{name: "vite", script: "vite --open", deps: map[string]string{"vite": "6"}, want: "Vite"},
		{name: "vite dev", script: "vite dev", deps: map[string]string{"vite": "6"}, want: "Vite"},
		{name: "vite serve", script: "vite serve", deps: map[string]string{"vite": "6"}, want: "Vite"},
		{name: "svelte before vite", script: "vite dev", deps: map[string]string{"@sveltejs/kit": "2", "vite": "6"}, want: "SvelteKit"},
		{name: "astro", script: "astro dev", deps: map[string]string{"astro": "5"}, want: "Astro"},
		{name: "nuxt", script: "nuxt dev", deps: map[string]string{"nuxt": "4"}, want: "Nuxt"},
		{name: "react router", script: "react-router dev", deps: map[string]string{"@react-router/dev": "7", "vite": "6"}, want: "React Router"},
		{name: "remix", script: "remix vite:dev", deps: map[string]string{"@remix-run/dev": "2", "vite": "6"}, want: "Remix"},
		{name: "inferred next", deps: map[string]string{"next": "16"}, want: "Next.js"},
		{name: "inferred vite", deps: map[string]string{"vite": "6"}, want: "Vite"},
		{name: "opaque dev script", script: "node server.js", deps: map[string]string{"vite": "6"}},
		{name: "production", script: "next start", deps: map[string]string{"next": "16"}},
		{name: "build", script: "vite build", deps: map[string]string{"vite": "6"}},
		{name: "preview", script: "vite preview", deps: map[string]string{"vite": "6"}},
		{name: "missing dependency", script: "vite"},
		{name: "shell chain", script: "vite && node server.js", deps: map[string]string{"vite": "6"}},
		{name: "background", script: "vite &", deps: map[string]string{"vite": "6"}},
		{name: "wrapper", script: "cross-env DEBUG=1 vite", deps: map[string]string{"vite": "6"}},
		{name: "assignment", script: "DEBUG=1 vite", deps: map[string]string{"vite": "6"}},
		{name: "expansion", script: "vite $OPTIONS", deps: map[string]string{"vite": "6"}},
		{name: "newline", script: "vite\nnode server.js", deps: map[string]string{"vite": "6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := map[string]any{"devDependencies": tc.deps}
			if tc.script != "" {
				pkg["scripts"] = map[string]string{"dev": tc.script}
			}
			data, err := json.Marshal(pkg)
			if err != nil {
				t.Fatal(err)
			}
			server, ok := frameworkprofile.DevServerCommand(fstest.MapFS{"package.json": &fstest.MapFile{Data: data}})
			if !ok || server.HMRFramework != tc.want {
				t.Fatalf("server = %+v, ok=%v; want HMR framework %q", server, ok, tc.want)
			}
			if tc.script != "" && (server.Command != "npm run dev" || server.Framework != "dev script") {
				t.Fatalf("changed shared script contract: %+v", server)
			}
		})
	}
}
