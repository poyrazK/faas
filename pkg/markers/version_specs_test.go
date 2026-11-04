package markers

import (
	"slices"
	"testing"
)

// ADR-1500 amendment 2: the Python shared runtime choice must see every
// declaration Railpack or mise could honor, as written.
func TestPythonVersionSpecsAtRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		root  string
		want  []string
	}{
		{"none", map[string]string{"requirements.txt": "flask\n"}, "", nil},
		{"python-version first line", map[string]string{".python-version": "# pinned\n3.12\n3.11\n"}, "", []string{"3.12"}},
		{"runtime.txt", map[string]string{"runtime.txt": "python-3.12.4\n"}, "", []string{"python-3.12.4"}},
		{"pipfile", map[string]string{"Pipfile": "[requires]\npython_version = \"3.13\"\n"}, "", []string{"3.13"}},
		{"tool-versions", map[string]string{".tool-versions": "nodejs 22\npython 3.11.9 3.12.1\n"}, "", []string{"3.11.9"}},
		{"mise string", map[string]string{"mise.toml": "[tools]\npython = \"3.12\"\n"}, "", []string{"3.12"}},
		{"mise array kept raw", map[string]string{".mise.toml": "[tools]\npython = [\"3.12\", \"3.11\"]\n"}, "", []string{`["3.12", "3.11"]`}},
		{"railpack package", map[string]string{"railpack.json": `{"packages":{"python":"3.11"}}`}, "", []string{"3.11"}},
		{"every source", map[string]string{".python-version": "3.12\n", "runtime.txt": "python-3.13.1"}, "", []string{"3.12", "python-3.13.1"}},
		{"workspace member", map[string]string{".python-version": "3.11", "svc/.python-version": "3.12.2"}, "svc", []string{"3.12.2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tempTarball(t)
			writeTarGz(t, path, tc.files)
			if got := PythonVersionSpecsAtRoot(path, tc.root); !slices.Equal(got, tc.want) {
				t.Fatalf("PythonVersionSpecsAtRoot = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRailpackPackageVersionAtRoot(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"absent file", "", ""},
		{"no packages", `{"deploy":{}}`, ""},
		{"string", `{"packages":{"node":"20"}}`, "20"},
		{"non-string raw", `{"packages":{"node":20}}`, "20"},
		{"other package", `{"packages":{"python":"3.12"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"package.json": "{}"}
			if tc.body != "" {
				files["railpack.json"] = tc.body
			}
			path := tempTarball(t)
			writeTarGz(t, path, files)
			if got := RailpackPackageVersionAtRoot(path, "", "node"); got != tc.want {
				t.Fatalf("RailpackPackageVersionAtRoot = %q, want %q", got, tc.want)
			}
		})
	}
}
