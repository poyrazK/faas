package fcvm

import (
	"errors"
	"path/filepath"
	"strings"
)

func nativeHostHelperParent(raw string) (string, error) {
	line := strings.TrimSuffix(raw, "\n")
	path, ok := strings.CutPrefix(line, "0::/")
	if !ok || filepath.IsAbs(path) || strings.ContainsAny(path, "\n\r\\") || path != "" && filepath.Clean(path) != path {
		return "", errors.New("native helper: ambiguous daemon cgroup membership")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." || part == "" && path != "" {
			return "", errors.New("native helper: invalid daemon cgroup path")
		}
	}
	return path, nil
}

func parseNativeHelperCgroupPopulated(raw string) (bool, error) {
	seen := make(map[string]bool)
	var populated bool
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || seen[parts[0]] || parts[1] != "0" && parts[1] != "1" {
			return false, errors.New("native helper: malformed cgroup events")
		}
		seen[parts[0]] = true
		switch parts[0] {
		case "populated":
			populated = parts[1] == "1"
		case "frozen":
		default:
			return false, errors.New("native helper: unknown cgroup event")
		}
	}
	if !seen["populated"] {
		return false, errors.New("native helper: missing populated cgroup event")
	}
	return populated, nil
}
