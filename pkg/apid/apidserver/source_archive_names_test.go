// spec: §4.2
// adr: 521
package apidserver_test

import (
	"archive/tar"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceRefArchiveNameNormalization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
		root   string
	}{
		{"wrapper", "repo/", ""},
		{"repeated dot", "././repo/", ""},
		{"workspace repeated dot", "././repo/apps/api/", "apps/api"},
		{"hidden wrapper", ".repo/", ""},
		{"wrapper ending in dot", "repo./", ""},
		{"hidden workspace", "repo/.apps/api/", ".apps/api"},
		{"workspace ending in dot", "repo/apps/api./", "apps/api."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := operationSchemaArchive(t, []operationSchemaEntry{
				{tc.prefix + "gregale.yaml", "{}\n", tar.TypeReg},
				{tc.prefix + "Dockerfile", "FROM scratch\n", tar.TypeReg},
			})
			manifest, problem := apidserver.LoadSourceRefManifest(archive, state.App{Slug: "app", Type: state.AppTypeApp, RootDir: tc.root}, api.PlanPro, false)
			if problem != nil || manifest == nil {
				t.Errorf("source manifest = %+v, %+v; want selected manifest", manifest, problem)
			}
			if tc.root != "" {
				if found, err := apidserver.ArchiveHasSourceRoot(archive, tc.root); err != nil || !found {
					t.Errorf("selected source root found = %v, %v; want true", found, err)
				}
			}
			if found, err := apidserver.ArchiveHasRootDockerfileAtRoot(archive, tc.root); err != nil || !found {
				t.Errorf("selected Dockerfile found = %v, %v; want true", found, err)
			}
		})
	}
}

func TestOperationSchemaArchiveRepeatedLeadingDot(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		name, prefix := "valid archive", "././"
		if duplicate {
			name, prefix = "duplicate alias", ""
		}
		t.Run(name, func(t *testing.T) {
			entries := []operationSchemaEntry{
				{prefix + "repo/apps/api/input.json", `{"type":"object"}`, tar.TypeReg},
				{prefix + "repo/apps/api/output.json", "true", tar.TypeReg},
			}
			if duplicate {
				entries = append(entries, operationSchemaEntry{"././repo/apps/api/input.json", "false", tar.TypeReg})
			}
			manifest := &gregalemanifest.Manifest{Operations: []gregalemanifest.Operation{{
				HTTPTransactionVersion: 1, Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
				InputSchema: "input.json", OutputSchema: "output.json", ProgressStages: []string{"generating"},
			}}}
			err := apidserver.ResolveSourceOperations(operationSchemaArchive(t, entries), "repo/apps/api/gregale.yaml", "app", api.PlanPro, manifest)
			if (err != nil) != duplicate {
				t.Errorf("resolve schemas: %v; want duplicate rejection=%v", err, duplicate)
			}
		})
	}
}
