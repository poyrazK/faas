package statelessshape

import (
	"encoding/json"
	"path"
	"strings"
)

// scratchRoots hold only scratch data that no app expects to keep: a VOLUME
// under one of them loses nothing when the app parks. Images such as Spring
// Boot's declare `VOLUME /tmp`, and refusing them made agents edit working
// Dockerfiles for no safety gain (2026-11-15 agent eval).
var scratchRoots = []string{"/tmp", "/var/tmp", "/run", "/var/run", "/dev/shm"}

// StatefulVolume parses a Dockerfile VOLUME instruction (`VOLUME /data`,
// `VOLUME ["/data", "/tmp"]`) and returns the paths in it that are not
// scratch space. A VOLUME is stateful when that list is non-empty; one that
// names no path at all is treated as stateful.
func StatefulVolume(instruction string) []string {
	fields := strings.Fields(strings.TrimSpace(instruction))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "VOLUME") {
		return nil
	}
	rest := strings.TrimSpace(strings.TrimSpace(instruction)[len(fields[0]):])
	var paths []string
	if strings.HasPrefix(rest, "[") {
		if err := json.Unmarshal([]byte(rest), &paths); err != nil {
			return []string{rest}
		}
	} else {
		paths = strings.Fields(rest)
	}
	if len(paths) == 0 {
		return []string{"(no path)"}
	}
	var stateful []string
	for _, p := range paths {
		if !scratchPath(p) {
			stateful = append(stateful, p)
		}
	}
	return stateful
}

func scratchPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "$") {
		return false
	}
	clean := path.Clean(p)
	for _, root := range scratchRoots {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return true
		}
	}
	return false
}
