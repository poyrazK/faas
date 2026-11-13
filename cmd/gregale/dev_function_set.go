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
	return fmt.Errorf("this is a functions project (%s), and a developer environment runs one app. Run every function on this machine with `gregale dev --local` (it uses your developer environment's database and buckets), or one function remotely with `gregale dev --path <its root>`", strings.Join(names, ", "))
}
