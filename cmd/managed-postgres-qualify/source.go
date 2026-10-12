package main

import (
	"encoding/hex"
	"runtime/debug"
	"strings"
)

func qualificationExecutableSource() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	return qualificationBuildSource(info)
}

func qualificationBuildSource(info *debug.BuildInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	revision := settings["vcs.revision"]
	_, err := hex.DecodeString(revision)
	if settings["vcs"] != "git" || len(revision) != 40 || strings.ToLower(revision) != revision || err != nil {
		return "", false
	}
	return revision, settings["vcs.modified"] == "false"
}
