//go:build linux

package main

import (
	"bytes"
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
	var out []reseedCandidate
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || pid == 1 {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		exe, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil || reseedRuntimeForExe(exe) == "" {
			continue
		}
		c := reseedCandidate{pid: pid, exe: exe}
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
