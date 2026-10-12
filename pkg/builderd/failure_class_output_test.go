// spec: §6.4
package builderd

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #9: a missing npm package failed as a bare "build exited
// 1"; guest-init never stamps BuildDone.FailureCode, so dep_install_failed and
// app_arch_mismatch never fired.
func TestClassifyBuildOutputNamesTheFailingInstall(t *testing.T) {
	daemon := "\n[buildkitd]\n" + strings.Repeat("exec format error\n", 3)
	for _, tc := range []struct {
		name, out, code, pkg string
	}{
		{name: "npm 404", out: "#7 4.1 npm error code E404\n#7 4.1 npm error 404 Not Found - GET https://registry.npmjs.org/h9-x", code: api.CodeDepInstallFailed, pkg: "npm"},
		{name: "npm legacy", out: "npm ERR! code ETARGET\nnpm ERR! notarget No matching version found", code: api.CodeDepInstallFailed, pkg: "npm"},
		{name: "pnpm", out: " ERR_PNPM_FETCH_404  GET https://registry.npmjs.org/x: Not Found - 404", code: api.CodeDepInstallFailed, pkg: "pnpm"},
		{name: "pip", out: "ERROR: Could not find a version that satisfies the requirement h9-x==1.0\nERROR: No matching distribution found for h9-x==1.0", code: api.CodeDepInstallFailed, pkg: "pip"},
		{name: "cargo", out: "error: no matching package named `h9-x` found", code: api.CodeDepInstallFailed, pkg: "cargo"},
		{name: "go", out: "go: example.com/h9@v1.2.3: reading https://proxy.golang.org/example.com/h9/@v/v1.2.3.mod: 404 Not Found", code: api.CodeDepInstallFailed, pkg: "go"},
		{name: "arch", out: "/bin/sh: ./server: cannot execute binary file: exec format error", code: api.CodeAppArchMismatch},
		{name: "plain RUN failure", out: "#6 0.2 h9 compiling\n#6 ERROR: process \"/bin/sh -c exit 3\" did not complete successfully: exit code: 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, pkg := classifyBuildOutput("[build]\n" + tc.out + daemon)
			if code != tc.code || pkg != tc.pkg {
				t.Fatalf("classifyBuildOutput = (%q, %q), want (%q, %q)", code, pkg, tc.code, tc.pkg)
			}
		})
	}
}
