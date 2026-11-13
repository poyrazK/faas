//go:build linux

package main

import (
	"bytes"
	"debug/buildinfo"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// scanReseedCandidates reads every process in /proc except guest-init itself.
// Unreadable entries (a process that exited, a kernel thread) are skipped.
func scanReseedCandidates(procRoot string, self int) []reseedCandidate {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	selfExe, _ := os.Readlink(filepath.Join(procRoot, strconv.Itoa(self), "exe"))
	var out []reseedCandidate
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || pid == 1 {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		exe, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil {
			continue
		}
		c := reseedCandidate{pid: pid, exe: exe}
		if reseedRuntimeForExe(exe) == "" {
			// A Go executable is named by nothing in its path. Only a
			// workload process (guest-init stamped the barrier socket into
			// its environment) counts, never guest-init's own helpers.
			if exe == selfExe || !stampedForReseed(dir) || !goExecutable(filepath.Join(dir, "exe")) {
				continue
			}
			c.runtime = "go"
		}
		if cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			if args := bytes.Split(bytes.TrimRight(cmdline, "\x00"), []byte{0}); len(args) > 1 {
				c.entry = string(args[1])
				if !filepath.IsAbs(c.entry) {
					if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil {
						c.entry = filepath.Join(cwd, c.entry)
					}
				}
				if real, err := filepath.EvalSymlinks(c.entry); err == nil {
					c.entry = real
				}
			}
		}
		if environ, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
			for _, kv := range bytes.Split(environ, []byte{0}) {
				if k, v, ok := strings.Cut(string(kv), "="); ok && k == restoreReseedOptOutEnv && strings.EqualFold(v, "off") {
					c.optedOut = true
				}
			}
		}
		if root, err := os.Readlink(filepath.Join(dir, "root")); err == nil && root != "/" {
			c.ownRoot = true
		}
		out = append(out, c)
	}
	return out
}

// unregisteredRuntimeProcesses reports the interpreter processes the last
// barrier pass did not reach. Only app guests run the barrier.
func unregisteredRuntimeProcesses() (int, []string) {
	b := activeRestoreReseedBarrier.Load()
	if b == nil {
		return 0, nil
	}
	return unregisteredRuntimes(scanReseedCandidates("/proc", os.Getpid()), b.registeredPIDs())
}

// stampedForReseed reports whether guest-init stamped the barrier socket into
// the process environment, which marks a workload process.
func stampedForReseed(dir string) bool {
	environ, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return false
	}
	return bytes.Contains(append([]byte{0}, environ...), []byte("\x00"+restoreReseedSocketEnv+"="))
}

// goExecutable reports whether the executable carries Go build information.
func goExecutable(path string) bool {
	_, err := buildinfo.ReadFile(path)
	return err == nil
}

// goIDLibraryModules lists the Go modules, in Go workload processes, whose
// IDs repeat across instances restored from one snapshot (ADR-20261110170000):
// rs/xid and the MongoDB driver's ObjectID draw a per-process random value
// once, and the OpenTelemetry SDK seeds its trace and span ID generator once.
// Nothing outside the program can reset them.
func goIDLibraryModules(procRoot string, self int) []string {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	selfExe, _ := os.Readlink(filepath.Join(procRoot, strconv.Itoa(self), "exe"))
	found := map[string]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || pid == 1 {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		exe, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil || exe == selfExe || !stampedForReseed(dir) {
			continue
		}
		info, err := buildinfo.ReadFile(filepath.Join(dir, "exe"))
		if err != nil {
			continue
		}
		for _, dep := range info.Deps {
			if module := goIDLibrary(dep.Path); module != "" {
				found[module] = true
			}
		}
	}
	return sortedKeys(found)
}
