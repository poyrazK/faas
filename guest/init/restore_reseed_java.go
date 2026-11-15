package main

import (
	"os"
	"path/filepath"
	"strings"
)

// restoreReseedImageRoot is the workload's root as guest-init sees it when it
// stamps the environment: the app image after pivot_root. Tests point it at a
// fake image.
var restoreReseedImageRoot = "/"

// javaHomeGlobs are the usual places a JVM lives in an image. Checked in
// addition to the command, JAVA_HOME and PATH, so a launcher script that runs
// a JVM by absolute path is still seen.
var javaHomeGlobs = []string{
	"/opt/*/bin/java",
	"/opt/*/*/bin/java",
	"/usr/lib/jvm/*/bin/java",
	"/usr/local/*/bin/java",
	"/app/*/bin/java",
}

// javaWithoutInstrument returns the first JVM in the image that cannot load a
// -javaagent: one built with jlink without the java.instrument module has no
// libinstrument. Naming any agent in JAVA_TOOL_OPTIONS stops such a JVM from
// starting ("Could not find agent library instrument"), so the reseed agent
// must be left out for the whole workload. "" means every JVM found can load
// it, or none was found.
func javaWithoutInstrument(root string, env []string, commands []string) string {
	seen := map[string]bool{}
	for _, java := range javaCandidates(root, env, commands) {
		bin, ok := resolveInRoot(root, java)
		if !ok || seen[bin] {
			continue
		}
		seen[bin] = true
		home := filepath.Dir(filepath.Dir(bin))
		if !hasInstrument(root, home) {
			return bin
		}
	}
	return ""
}

func javaCandidates(root string, env []string, commands []string) []string {
	var out []string
	for _, c := range commands {
		if filepath.Base(c) != "java" {
			continue
		}
		if strings.Contains(c, "/") {
			out = append(out, c)
		} else {
			out = append(out, pathLookupInRoot(root, "java", env)...)
		}
	}
	if home := envValue(env, "JAVA_HOME"); filepath.IsAbs(home) {
		out = append(out, filepath.Join(home, "bin", "java"))
	}
	out = append(out, pathLookupInRoot(root, "java", env)...)
	for _, pattern := range javaHomeGlobs {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err == nil {
				out = append(out, "/"+rel)
			}
		}
	}
	return out
}

func pathLookupInRoot(root, name string, env []string) []string {
	var out []string
	for _, dir := range strings.Split(envValue(env, "PATH"), ":") {
		if !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if info, err := os.Stat(filepath.Join(root, p)); err == nil && !info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// resolveInRoot follows symlinks one component at a time inside root, so an
// absolute link target (Debian's /usr/bin/java -> /etc/alternatives/java ->
// /usr/lib/jvm/...) stays in the image. It returns the image path of a
// regular file.
func resolveInRoot(root, p string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(p), "/"), "/")
	cur := "/"
	for hops := 0; len(parts) > 0; {
		next := filepath.Join(cur, parts[0])
		parts = parts[1:]
		info, err := os.Lstat(filepath.Join(root, next))
		if err != nil {
			return "", false
		}
		if info.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if hops++; hops > 40 {
			return "", false
		}
		target, err := os.Readlink(filepath.Join(root, next))
		if err != nil {
			return "", false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(cur, target)
		}
		parts = append(strings.Split(strings.TrimPrefix(filepath.Clean(target), "/"), "/"), parts...)
		cur = "/"
	}
	info, err := os.Stat(filepath.Join(root, cur))
	if err != nil || info.IsDir() {
		return "", false
	}
	return cur, true
}

// hasInstrument reports whether the JVM at home ships libinstrument: lib/ on
// Java 9+, lib/<arch>/ and jre/lib/<arch>/ on Java 8.
func hasInstrument(root, home string) bool {
	for _, pattern := range []string{
		"lib/libinstrument.so",
		"lib/*/libinstrument.so",
		"jre/lib/*/libinstrument.so",
		"lib/libinstrument.dylib",
	} {
		matches, _ := filepath.Glob(filepath.Join(root, home, pattern))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}
