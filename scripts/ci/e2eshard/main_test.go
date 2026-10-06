package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestAssignedShardPartitionsEveryTestExactlyOnce(t *testing.T) {
	tests := []string{"TestApplyProject_Diff_Changed", "TestScanProject_MultiTier", "TestWebhookE2E_Retry"}
	const shards = 4
	seen := make(map[string]int)
	for shard := 0; shard < shards; shard++ {
		for _, name := range tests {
			if assignedShard(name, shards) == shard {
				seen[name]++
			}
		}
	}
	for _, name := range tests {
		if seen[name] != 1 {
			t.Fatalf("%s assigned %d times", name, seen[name])
		}
	}
}

func TestVerifyRegisteredRejectsOmittedFuzzSeedsAndInventedTests(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inventory []string
		output    string
		wantError bool
	}{
		{"complete", []string{"TestValue", "FuzzWalk", "Example"}, "TestValue\nFuzzWalk\nExample\nok fixture 0.001s\n", false},
		{"omitted fuzz", []string{"TestValue"}, "TestValue\nFuzzWalk\n", true},
		{"invented test", []string{"TestValue", "TestMissing"}, "TestValue\n", true},
		{"duplicate registered", []string{"TestValue"}, "TestValue\nTestValue\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registered.txt")
			if err := os.WriteFile(path, []byte(tc.output), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := verifyRegistered(tc.inventory, path); (err != nil) != tc.wantError {
				t.Fatalf("verifyRegistered error = %v, want error = %v", err, tc.wantError)
			}
		})
	}
}

func TestPartitionBootTests(t *testing.T) {
	regular, boot := partitionBootTests([]string{
		"TestApplyProject_Diff_Changed",
		"TestBootContract_APIDRenderedConfigAndProductionListeners",
	})
	if !reflect.DeepEqual(regular, []string{"TestApplyProject_Diff_Changed"}) ||
		!reflect.DeepEqual(boot, []string{"TestBootContract_APIDRenderedConfigAndProductionListeners"}) {
		t.Fatalf("regular=%v boot=%v", regular, boot)
	}
}

func TestIsGoTestName(t *testing.T) {
	for _, name := range []string{"Test", "TestApplyProject", "Test_Edge", "Test1", "TestÉdge"} {
		if !isGoTestName(name) {
			t.Errorf("isGoTestName(%q)=false", name)
		}
	}
	for _, name := range []string{"TestMain", "Tester", "Testé", "BenchmarkApply", "helper"} {
		if isGoTestName(name) {
			t.Errorf("isGoTestName(%q)=true", name)
		}
	}
}

func TestAllRunnableNamesIncludeFuzzSeedsAndExamples(t *testing.T) {
	const source = `package fixture
import "testing"
func TestMain(m *testing.M) {}
func TestOrdinary(t *testing.T) {}
func TestBootContract_Fixture(t *testing.T) {}
func FuzzWalk(f *testing.F) {}
func Fuzzer() {}
func BenchmarkWalk(b *testing.B) {}
func Example() {
 // Output: value
}
func Example_empty() {
 // Output:
}
func Example_unordered() {
 // Unordered output: value
}
func Example_documentationOnly() {}
`
	parsed, err := parser.ParseFile(token.NewFileSet(), "fixture_test.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	all := runnableNames(parsed, true)
	sort.Strings(all)
	want := []string{"Example", "Example_empty", "Example_unordered", "FuzzWalk", "TestBootContract_Fixture", "TestOrdinary"}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("all runnable names = %v, want %v", all, want)
	}
	regular, boot := partitionBootTests(runnableNames(parsed, false))
	if !reflect.DeepEqual(regular, []string{"TestOrdinary"}) || !reflect.DeepEqual(boot, []string{"TestBootContract_Fixture"}) {
		t.Fatalf("default E2E partition changed: regular=%v boot=%v", regular, boot)
	}
	seen := make(map[string]int)
	for shard := 0; shard < 2; shard++ {
		for _, name := range all {
			if assignedShard(name, 2) == shard {
				seen[name]++
			}
		}
	}
	for _, name := range want {
		if seen[name] != 1 {
			t.Fatalf("%s appears in %d shards", name, seen[name])
		}
	}
}
