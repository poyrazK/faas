package main

import (
	"os"
	"strings"
)

// warnRootHealthPath says why --health-path / is a mistake: the edge answers
// GET and HEAD on the health path without the app (unless
// --health-path-wakes), so visitors to the home page get
// {"status":"ok","source":"edge"} instead of the app (agent eval baseline
// 2026-11-17). The change still applies; the owner may want it.
func warnRootHealthPath(path string) {
	if hint := rootHealthPathHint(path); hint != "" {
		PrintWarn(os.Stderr, "%s", hint)
	}
}

func rootHealthPathHint(path string) string {
	if strings.TrimSpace(path) != "/" {
		return ""
	}
	return `--health-path / hides the home page: Gregale answers the health path at its edge, so visitors to / get {"status":"ok"} instead of the app. Use a route such as /healthz.`
}
