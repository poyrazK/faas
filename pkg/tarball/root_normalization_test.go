// spec: §4.2
package tarball

import "testing"

func TestRootPrefixRepeatedLeadingDot(t *testing.T) {
	archive := writeSourceArchive(t, map[string]string{"././project/apps/api/package.json": "{}"}, false, false)
	if root, err := RootPrefix(archive); err != nil || root != "project" {
		t.Errorf("RootPrefix = %q, %v; want project", root, err)
	}
	if root, err := ResolveSourceRoot(archive, "apps/api"); err != nil || root != "project/apps/api" {
		t.Errorf("ResolveSourceRoot = %q, %v; want project/apps/api", root, err)
	}
	if root, err := ResolveSourceRoot(archive, "project/apps/api"); err != nil || root != "project/apps/api" {
		t.Errorf("ResolveSourceRoot direct path = %q, %v; want project/apps/api", root, err)
	}
}
