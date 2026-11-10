package gregalemanifest

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

// readFunctionHooks returns the hooks declared by a function's handler file
// in fsys (handler relative to root, or the root's conventional handler.js,
// handler.mjs or handler.py when handler is not a file). A missing, oversized
// or non-Node/Python file declares none.
func readFunctionHooks(fsys fs.FS, root, handler string) []string {
	for _, rel := range append([]string{handler}, conventionNames...) {
		name := path.Clean(path.Join(root, rel))
		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > InlineConfigMaxFileBytes {
			return nil
		}
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil
		}
		return DetectFunctionHooks(name, src)
	}
	return nil
}

// HookLifecycleUpdate is the change that brings an app's lifecycle hooks to
// the snapshot hooks the function declares in code (ADR-20261110190000 §5):
// a declared beforeSnapshot or afterRestore points the hook at the runner's
// reserved path with the default timeout, and a reserved path the code no
// longer declares is cleared ({} clears a hook in an app PATCH). init needs no
// lifecycle hook. A hook the app points at a path of its own while the code
// also declares it is refused: one source per hook (§6).
func (fn FunctionSpec) HookLifecycleUpdate(currentAfter *api.AfterRestoreHook, currentBefore *api.BeforeCheckpointHook) (after *api.AfterRestoreHook, before *api.BeforeCheckpointHook, changed bool, err error) {
	wantAfter := slices.Contains(fn.Hooks, FunctionHookAfterRestore)
	wantBefore := slices.Contains(fn.Hooks, FunctionHookBeforeSnapshot)
	afterPath, beforePath := "", ""
	if currentAfter != nil {
		afterPath = currentAfter.Path
	}
	if currentBefore != nil {
		beforePath = currentBefore.Path
	}
	if wantAfter && afterPath != "" && afterPath != functionlifetime.FunctionAfterRestorePath {
		return nil, nil, false, fmt.Errorf("function %q declares afterRestore in code and its app also has an after_restore hook at %s; keep one: remove the app's hook or the code's", fn.Name, afterPath)
	}
	if wantBefore && beforePath != "" && beforePath != functionlifetime.FunctionBeforeSnapshotPath {
		return nil, nil, false, fmt.Errorf("function %q declares beforeSnapshot in code and its app also has a before_checkpoint hook at %s; keep one: remove the app's hook or the code's", fn.Name, beforePath)
	}
	switch {
	case wantAfter && afterPath != functionlifetime.FunctionAfterRestorePath:
		after, changed = &api.AfterRestoreHook{Path: functionlifetime.FunctionAfterRestorePath}, true
	case !wantAfter && afterPath == functionlifetime.FunctionAfterRestorePath:
		after, changed = &api.AfterRestoreHook{}, true
	}
	switch {
	case wantBefore && beforePath != functionlifetime.FunctionBeforeSnapshotPath:
		before, changed = &api.BeforeCheckpointHook{Path: functionlifetime.FunctionBeforeSnapshotPath}, true
	case !wantBefore && beforePath == functionlifetime.FunctionBeforeSnapshotPath:
		before, changed = &api.BeforeCheckpointHook{}, true
	}
	return after, before, changed, nil
}

// FunctionHooksInDir returns the snapshot hooks a single function's handler
// declares, read from dir the way a functions project reads each entry
// (ADR-20261110190000 §5). handler is relative to dir; empty uses the
// conventional handler file.
func FunctionHooksInDir(dir, handler string) []string {
	if dir == "" {
		return nil
	}
	return readFunctionHooks(os.DirFS(dir), ".", handler)
}
