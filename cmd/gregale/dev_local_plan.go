package main

// Planning and preparing `gregale dev --local` (ADR-20261107210000): which
// functions the project has, and the process that runs each one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/guest/runners/local"
	"github.com/onebox-faas/faas/pkg/fnadapter"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// localFunction is one function served by `gregale dev --local`.
type localFunction struct {
	Name    string
	Root    string // absolute
	Family  string // node, python or go
	Runtime string
	// HandlerFile is the Node or Python source the adapter loads (absolute).
	HandlerFile string
	Entry       bool
	Routes      []string
	Env         map[string]string
	TimeoutS    int
	Crons       []string
	proc        local.Function
}

func runtimeFamily(runtime string) string {
	switch {
	case strings.HasPrefix(runtime, "node"):
		return "node"
	case strings.HasPrefix(runtime, "python"):
		return "python"
	case strings.HasPrefix(runtime, "go"):
		return "go"
	}
	return ""
}

// planLocalFunctions lists the project's functions: its functions: manifest
// (with config in code where supported), or the directory itself when it is
// one function.
func planLocalFunctions(projectDir string) ([]*localFunction, error) {
	m, isSet, err := functionSetManifest(projectDir)
	if err != nil {
		return nil, err
	}
	if !isSet {
		runtime, handler, ok := inferFunctionRuntime(projectDir)
		if !ok {
			return nil, errors.New("no function found: add functions to gregale.yaml, or run from a directory with handler.js, handler.py or handler.go")
		}
		spec := gregalemanifest.FunctionSpec{Name: localName(projectDir), Runtime: runtime}
		if h := strings.TrimSpace(handler); h != "" && h != defaultTemplateHandler {
			spec.Handler = h
		}
		fn, err := newLocalFunction(spec, projectDir, nil)
		if err != nil {
			return nil, err
		}
		return []*localFunction{fn}, nil
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	fns := make([]*localFunction, 0, len(m.Functions))
	for _, spec := range m.Functions {
		var crons []string
		for _, t := range m.Triggers {
			if t.Kind == gregalemanifest.TriggerKindCron && t.App == spec.Name {
				crons = append(crons, t.Schedule)
			}
		}
		fn, err := newLocalFunction(spec, filepath.Join(projectDir, filepath.FromSlash(spec.FunctionRoot())), crons)
		if err != nil {
			return nil, fmt.Errorf("function %q: %w", spec.Name, err)
		}
		fns = append(fns, fn)
	}
	return fns, nil
}

// localName turns a directory name into a host label.
func localName(dir string) string {
	name := strings.ToLower(filepath.Base(dir))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "function"
}

func newLocalFunction(spec gregalemanifest.FunctionSpec, absRoot string, crons []string) (*localFunction, error) {
	runtime, handler, err := functionRuntimeAndHandler(spec, absRoot)
	if err != nil {
		return nil, err
	}
	fn := &localFunction{
		Name: spec.Name, Root: absRoot, Family: runtimeFamily(runtime), Runtime: runtime,
		Entry: spec.Entry, Routes: spec.Routes, Env: spec.Env, TimeoutS: spec.RequestTimeoutS, Crons: crons,
	}
	switch fn.Family {
	case "node", "python":
		fn.HandlerFile, err = localHandlerFile(absRoot, spec.Handler, handler, fn.Family)
		if err != nil {
			return nil, err
		}
	case "go":
	default:
		return nil, fmt.Errorf("runtime %q does not run locally yet; use gregale dev", runtime)
	}
	return fn, nil
}

// localHandlerFile is the source the adapter loads: the handler: file, else
// the root's conventional handler.
func localHandlerFile(absRoot, explicit, wire, family string) (string, error) {
	if explicit != "" {
		return filepath.Join(absRoot, filepath.FromSlash(explicit)), nil
	}
	names := []string{"handler.js", "handler.mjs"}
	if family == "python" {
		names = []string{"handler.py"}
	}
	if wire != "" && wire != defaultTemplateHandler && filepath.Ext(wire) != "" {
		names = append([]string{wire}, names...)
	}
	for _, name := range names {
		path := filepath.Join(absRoot, filepath.FromSlash(name))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("no %s in %s", strings.Join(names, " or "), absRoot)
}

// localCacheDir holds adapters, Go builds and the session state, outside the
// project so nothing is added to the working tree.
func localCacheDir(projectDir string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(projectDir))
	return filepath.Join(base, "gregale", "dev-local", hex.EncodeToString(sum[:8])), nil
}

// prepare writes the adapter or builds the Go handler, and sets the process
// the runner core starts.
func (fn *localFunction) prepare(ctx context.Context, cacheDir string, baseEnv []string) error {
	dir := filepath.Join(cacheDir, fn.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	env := append(append([]string(nil), baseEnv...), "FAAS_RUNTIME="+fn.Runtime, "GREGALE_LOCAL=1")
	keys := make([]string, 0, len(fn.Env))
	for k := range fn.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+fn.Env[k])
	}
	switch fn.Family {
	case "node":
		return fn.prepareAdapter(dir, "adapter.cjs", "node", env, fnadapter.NodeFor)
	case "python":
		return fn.prepareAdapter(dir, "adapter.py", "python3", env, fnadapter.PythonFor)
	}
	bin, err := buildLocalGo(ctx, fn.Root, dir)
	if err != nil {
		return err
	}
	fn.proc = local.Function{Runtime: fn.Runtime, Executable: bin, Env: env, HandlerPath: bin, Compiled: true}
	return nil
}

func (fn *localFunction) prepareAdapter(dir, name, interpreter string, env []string, adapterFor func(string) string) error {
	exe, err := exec.LookPath(interpreter)
	if err != nil {
		return fmt.Errorf("%s is not installed; it runs %s functions locally", interpreter, fn.Family)
	}
	adapter := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, fn.HandlerFile)
	if err != nil {
		return err
	}
	if err := os.WriteFile(adapter, []byte(adapterFor(filepath.ToSlash(rel))), 0o600); err != nil {
		return err
	}
	fn.proc = local.Function{Runtime: fn.Runtime, Executable: exe, Args: []string{adapter}, Env: env, HandlerPath: adapter}
	return nil
}

// buildLocalGo builds the Go function in root. A root without go.mod (the
// function-go template) is built from a copy with the module file the CLI
// adds when it uploads one.
func buildLocalGo(ctx context.Context, root, dir string) (string, error) {
	goExe, err := exec.LookPath("go")
	if err != nil {
		return "", errors.New("go is not installed; it builds Go functions locally")
	}
	bin := filepath.Join(dir, "handler")
	buildDir := root
	if _, err := os.Stat(filepath.Join(root, "go.mod")); errors.Is(err, os.ErrNotExist) {
		buildDir = filepath.Join(dir, "src")
		if err := copyGoSources(root, buildDir); err != nil {
			return "", err
		}
	}
	cmd := exec.CommandContext(ctx, goExe, "build", "-o", bin, ".")
	cmd.Dir = buildDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build failed:\n%s", strings.TrimSpace(string(out)))
	}
	return bin, nil
}

func copyGoSources(root, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dst, "go.mod"), []byte(functionGoBuildModule), 0o600)
}
