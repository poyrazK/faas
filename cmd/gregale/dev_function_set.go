package main

import (
	"fmt"
	"strings"
)

// refuseDevFunctionSet explains why a remote `gregale dev` cannot take a
// functions project: a developer environment runs one app, and the project is
// several functions (gregale.yaml functions:, or functions/<name>/ handlers
// with config in code). Before this, the command said it found no source.
func refuseDevFunctionSet(sourceDir string) error {
	m, _, ok, err := functionSetManifestFromCode(sourceDir)
	if err != nil {
		return fmt.Errorf("read the project's functions config: %w", err)
	}
	if !ok {
		return nil
	}
	names := make([]string, 0, len(m.Functions))
	for _, fn := range m.Functions {
		names = append(names, fn.Name)
	}
	// A remote developer environment (and so its database and buckets) is
	// keyed to one app's directory, which a functions project root never
	// is; --offline gives the functions a local database and buckets instead.
	return fmt.Errorf("this is a functions project (%s), and a developer environment runs one app. Run every function on this machine with `gregale dev --local` (add --offline --postgres or --bucket NAME for a local database and buckets), or one function remotely with `gregale dev --path <its root>`", strings.Join(names, ", "))
}
