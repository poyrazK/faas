package storage

import (
	"fmt"
	"strings"
)

// planSnapshotKey retains legacy references while assigning each immutable
// capture part a distinct OCI tag in the deployment's existing repository.
func planSnapshotKey(key string, parts []string) (string, string, error) {
	if len(parts) < 3 || !depIDCharset.MatchString(parts[1]) {
		return "", "", fmt.Errorf("%w: invalid snapshot deployment in %q", ErrInvalidKey, key)
	}
	suffix := parts[2:]
	if suffix[0] == "warm" {
		suffix = suffix[1:]
	}
	valid := len(suffix) == 1 && snapshotPart(suffix[0])
	if len(suffix) == 3 {
		valid = suffix[0] == "captures" && depIDCharset.MatchString(suffix[1]) && snapshotPart(suffix[2])
	}
	if len(suffix) == 4 {
		valid = suffix[0] == "captures" && depIDCharset.MatchString(suffix[1]) && suffix[2] == "v2" && snapshotV2Part(suffix[3])
	}
	if !valid {
		return "", "", fmt.Errorf("%w: %q does not match a supported snapshot artifact key", ErrInvalidKey, key)
	}
	return "snap-" + parts[1], strings.Join(parts[2:], "-"), nil
}

func snapshotPart(part string) bool { return part == "mem" || part == "vmstate" }
func snapshotV2Part(part string) bool {
	return snapshotPart(part) || part == "drive"
}

func unplanSnapshotKey(dep, tag string) (string, bool) {
	if !depIDCharset.MatchString(dep) {
		return "", false
	}
	prefix := "snap/" + dep + "/"
	if strings.HasPrefix(tag, "warm-") {
		prefix += "warm/"
		tag = strings.TrimPrefix(tag, "warm-")
	}
	if snapshotPart(tag) {
		return prefix + tag, true
	}
	if !strings.HasPrefix(tag, "captures-") {
		return "", false
	}
	captureAndPart := strings.TrimPrefix(tag, "captures-")
	index := strings.LastIndexByte(captureAndPart, '-')
	if index < 0 {
		return "", false
	}
	capture, part := captureAndPart[:index], captureAndPart[index+1:]
	version := ""
	if strings.HasSuffix(capture, "-v2") {
		capture = strings.TrimSuffix(capture, "-v2")
		version = "v2/"
	}
	if !depIDCharset.MatchString(capture) || (version == "" && !snapshotPart(part)) || (version != "" && !snapshotV2Part(part)) {
		return "", false
	}
	return prefix + "captures/" + capture + "/" + version + part, true
}
