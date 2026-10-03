package reposcan

import (
	"fmt"
	"testing"
	"testing/fstest"
)

func TestComposePlatformTenantPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		want              *bool
	}{
		{name: "omitted"},
		{name: "required", declaration: "    x-gregale-platform-tenant-required: true\n", want: tenantPolicyBool(true)},
		{name: "disabled", declaration: "    x-gregale-platform-tenant-required: false\n", want: tenantPolicyBool(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"compose.yaml": &fstest.MapFile{Data: []byte(fmt.Sprintf("services:\n  customer-api:\n    build: .\n%s", tc.declaration))},
				"Dockerfile":   &fstest.MapFile{Data: []byte("FROM scratch\n")},
			}
			result, err := Scan(fsys)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Workloads) != 1 {
				t.Fatalf("workloads = %+v", result.Workloads)
			}
			got := result.Workloads[0].PlatformTenantRequired
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("policy = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComposePlatformTenantPolicyRejectsInvalidDeclarations(t *testing.T) {
	for _, service := range []string{
		"    image: postgres:16\n    x-gregale-platform-tenant-required: true\n",
		"    build: .\n    x-gregale-platform-tenant-required: maybe\n",
	} {
		_, err := Scan(fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte("services:\n  customer-api:\n" + service)}})
		if err == nil {
			t.Fatalf("accepted invalid policy: %s", service)
		}
	}
}

func tenantPolicyBool(v bool) *bool { return &v }
