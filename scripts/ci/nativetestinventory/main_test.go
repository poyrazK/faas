package main

// spec: §14 — native selection must follow Go constraints without executing guests.
import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testSource(t *testing.T, root, file, tag, declaration string) {
	t.Helper()
	dir := filepath.Join(root, "cmd/e2e")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := "//go:build " + tag + "\n\npackage e2e_test\n" + declaration + "\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGoConstraintsAndSyntax(t *testing.T) {
	root := t.TempDir()
	for _, fixture := range []struct{ file, tag, code string }{
		{"plain_test.go", "metal", "func TestPlain(t *testing.T) {}"},
		{"reordered_test.go", "managed_postgres_native && metal", "func TestPostgres(t *testing.T) {}"},
		{"ordered_test.go", "metal && object_storage_native", "func TestStorage(t *testing.T) {}"},
		{"negated_test.go", "metal && !object_storage_native", "func TestWithoutStorage(t *testing.T) {}"},
		{"windows_test.go", "metal && windows", "func TestWindows(t *testing.T) {}"},
		{"ordinary_test.go", "!no_pg", "func TestOrdinary(t *testing.T) {}"},
		{"syntax_test.go", "metal", "// func TestComment(t *testing.T) {}\nfunc TestMultiline(\n t *testing.T,\n) {}\nfunc Testhelper(t *testing.T) {}\nfunc TestWrong() {}\nfunc (x *Thing) TestMethod(t *testing.T) {}"},
	} {
		testSource(t, root, fixture.file, fixture.tag, fixture.code)
	}
	for _, tc := range []struct {
		tags string
		want []string
	}{
		{"metal", []string{"TestMultiline", "TestPlain", "TestWithoutStorage"}},
		{"metal,managed_postgres_native", []string{"TestMultiline", "TestPlain", "TestPostgres", "TestWithoutStorage"}},
		{"metal,object_storage_native", []string{"TestMultiline", "TestPlain", "TestStorage"}},
	} {
		t.Run(tc.tags, func(t *testing.T) {
			got, err := inventory(root, tc.tags, "")
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(got))
			for _, test := range got {
				names = append(names, test.Name)
			}
			if !slices.Equal(names, tc.want) {
				t.Fatalf("got %v want %v", names, tc.want)
			}
		})
	}
	got, err := inventory(root, "metal", "plain_test.go")
	if err != nil || len(got) != 1 || got[0].Name != "TestPlain" {
		t.Fatalf("file filter: %v %v", got, err)
	}
}

func TestInvalidSourcesAndDuplicatesFail(t *testing.T) {
	root := t.TempDir()
	testSource(t, root, "one_test.go", "metal", "func TestSame(t *testing.T) {}")
	testSource(t, root, "two_test.go", "metal", "func TestSame(t *testing.T) {}")
	if _, err := inventory(root, "metal", ""); err == nil {
		t.Fatal("accepted duplicate test")
	}
	testSource(t, root, "two_test.go", "metal", "func TestBroken(")
	if _, err := inventory(root, "metal", ""); err == nil {
		t.Fatal("accepted malformed source")
	}
}

func TestRepositoryNativeCoverageAndUnknownSuite(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if err := checkCoverage(root); err != nil {
		t.Fatal(err)
	}
	// Only copy source contracts into an owned temporary tree. No builds or KVM.
	probe := t.TempDir()
	for _, dir := range []string{"cmd/e2e", "scripts/ci", "pkg/e2etest/servicestarter", "pkg/e2etest/queuestarter", "pkg/e2etest/storagestarter", "pkg/managedpostgres/guestqualification"} {
		if err := os.MkdirAll(filepath.Join(probe, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"scripts/ci/binding-native-suites.json", "pkg/e2etest/servicestarter/contract.json", "pkg/e2etest/queuestarter/contract.json", "pkg/e2etest/storagestarter/contract.json", "pkg/managedpostgres/guestqualification/contract.json"} {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(probe, file), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ tag, name string }{{"managed_postgres_native", "TestManagedPostgresNativeMetal"}, {"object_storage_native", "TestObjectStorageNativeMetal"}, {"queue_binding_native", "TestQueueBindingNativeMetal"}, {"managed_service_native", "TestManagedServiceBindingNativeMetal"}} {
		testSource(t, probe, tc.tag+"_test.go", tc.tag+" && metal", "func "+tc.name+"(t *testing.T) {}")
	}
	if err := checkCoverage(probe); err != nil {
		t.Fatal(err)
	}
	testSource(t, probe, "future_test.go", "future_native && metal", "func TestFuture(t *testing.T) {}")
	if err := checkCoverage(probe); err == nil || !strings.Contains(err.Error(), "no configured suite") {
		t.Fatalf("unknown suite: %v", err)
	}
	if err := os.Remove(filepath.Join(probe, "cmd/e2e/future_test.go")); err != nil {
		t.Fatal(err)
	}
	testSource(t, probe, "extra_test.go", "metal && queue_binding_native", "func TestExtra(t *testing.T) {}")
	if err := checkCoverage(probe); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("runner omitted new test: %v", err)
	}
}
