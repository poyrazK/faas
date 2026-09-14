// Command e2eshard inventories the default cmd/e2e test package and emits the
// exact regular expression for one deterministic CI shard. It replaces manual
// prefix lists, which silently left entire test families outside CI.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type goPackage struct {
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
}

func main() {
	shard := flag.Int("shard", 0, "one-based shard to print")
	shards := flag.Int("shards", 4, "total regular E2E shards")
	check := flag.Bool("check", false, "validate and summarize the complete partition")
	pkg := flag.String("package", "./cmd/e2e", "Go package to inventory")
	flag.Parse()

	if *shards < 1 || *shard < 0 || *shard > *shards || (!*check && *shard == 0) {
		fmt.Fprintln(os.Stderr, "e2eshard: shard must be between 1 and shards")
		os.Exit(2)
	}
	tests, err := discover(*pkg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2eshard: %v\n", err)
		os.Exit(1)
	}
	regular, boot := partitionBootTests(tests)
	if len(boot) == 0 {
		fmt.Fprintln(os.Stderr, "e2eshard: no TestBootContract_ tests found for the dedicated production-config job")
		os.Exit(1)
	}
	if *check {
		counts := make([]int, *shards)
		for _, name := range regular {
			counts[assignedShard(name, *shards)]++
		}
		for i, count := range counts {
			if count == 0 {
				fmt.Fprintf(os.Stderr, "e2eshard: shard %d is empty\n", i+1)
				os.Exit(1)
			}
		}
		fmt.Printf("e2eshard: %d regular tests partitioned exactly once across %d shards (%v); %d boot-contract tests run separately\n", len(regular), *shards, counts, len(boot))
		if *shard == 0 {
			return
		}
	}

	selected := make([]string, 0, len(regular)/(*shards)+1)
	for _, name := range regular {
		if assignedShard(name, *shards) == *shard-1 {
			selected = append(selected, name)
		}
	}
	if len(selected) == 0 {
		fmt.Fprintf(os.Stderr, "e2eshard: shard %d is empty\n", *shard)
		os.Exit(1)
	}
	quoted := make([]string, len(selected))
	for i, name := range selected {
		quoted[i] = regexp.QuoteMeta(name)
	}
	fmt.Printf("^(%s)$\n", strings.Join(quoted, "|"))
}

func discover(pkg string) ([]string, error) {
	cmd := exec.Command("go", "list", "-json", pkg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w: %s", pkg, err, strings.TrimSpace(stderr.String()))
	}
	var listed goPackage
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, fmt.Errorf("decode go list output: %w", err)
	}
	files := append(append([]string(nil), listed.TestGoFiles...), listed.XTestGoFiles...)
	seen := make(map[string]string)
	tests := make([]string, 0)
	for _, file := range files {
		path := filepath.Join(listed.Dir, file)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isGoTestName(fn.Name.Name) {
				continue
			}
			if previous, exists := seen[fn.Name.Name]; exists {
				return nil, fmt.Errorf("duplicate test %s in %s and %s", fn.Name.Name, previous, file)
			}
			seen[fn.Name.Name] = file
			tests = append(tests, fn.Name.Name)
		}
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("no default-build tests found in %s", pkg)
	}
	sort.Strings(tests)
	return tests, nil
}

func isGoTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") || len(name) == len("Test") {
		return false
	}
	next := rune(name[len("Test")])
	return next < 'a' || next > 'z'
}

func partitionBootTests(tests []string) (regular, boot []string) {
	for _, name := range tests {
		if strings.HasPrefix(name, "TestBootContract_") {
			boot = append(boot, name)
		} else {
			regular = append(regular, name)
		}
	}
	return regular, boot
}

func assignedShard(name string, shards int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int(h.Sum32() % uint32(shards))
}
