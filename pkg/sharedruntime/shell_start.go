package sharedruntime

import "strings"

// ShellStart runs a start command through /bin/sh. A single simple command
// is exec'd so the app replaces the shell: guest-init signals only the
// process it started, and the runner base's busybox sh neither forwards
// SIGTERM nor execs the last command itself. Without exec a worker's
// SIGTERM handler never ran and the app was killed at the end of the stop
// grace period (prod hunt r57). Anything sh would treat as more than one
// command, or a first word exec cannot run, keeps the plain form.
func ShellStart(command string) []string {
	command = strings.TrimSpace(command)
	if execable(command) {
		command = "exec " + command
	}
	return []string{"/bin/sh", "-c", command}
}

// shellOnlyWords are keywords and builtins that exec cannot replace the shell
// with, or that change the shell itself.
var shellOnlyWords = map[string]bool{
	"exec": true, "cd": true, "export": true, "source": true, ".": true, "set": true,
	"unset": true, "eval": true, "trap": true, "ulimit": true, "umask": true,
	"if": true, "for": true, "while": true, "until": true, "case": true,
	"!": true, "{": true, "(": true, ":": true, "readonly": true, "local": true,
	"alias": true, "wait": true, "shift": true, "return": true, "exit": true,
}

func execable(command string) bool {
	if command == "" || strings.ContainsAny(command, ";&|\n(){}`") {
		return false
	}
	fields := strings.Fields(command)
	first := fields[0]
	// An env prefix (PORT=3000 node x) would make exec look up "PORT=3000".
	return !strings.Contains(first, "=") && !shellOnlyWords[first]
}
