package buildprogress

import (
	"strings"
	"unicode"
)

// StepLogPrefix starts every build-log line builderd writes for a BuildKit
// step as the build runs, so a client can find the newest step in the
// deployment's build log (retail-store feedback A3, 2026-11-17).
const StepLogPrefix = "build step: "

// StepTracker turns a live BuildKit plain-progress stream into one line per
// new build step. It sees each line once, in order, and keeps only the
// vertex numbers it has already reported, so the console's interleaved
// output, logs and statuses cost nothing.
type StepTracker struct {
	seen         map[int]struct{}
	emitted      int
	maxSteps     int
	maxNameBytes int
}

// NewStepTracker reports at most maxSteps steps, each name cut to
// maxNameBytes.
func NewStepTracker(maxSteps, maxNameBytes int) *StepTracker {
	return &StepTracker{seen: map[int]struct{}{}, maxSteps: maxSteps, maxNameBytes: maxNameBytes}
}

// Observe returns the step name when line is the first line of a BuildKit
// vertex worth showing a person: a Dockerfile instruction ("[3/12] RUN mvn
// package"), a Railpack step ("npm ci") or an export. BuildKit's own setup
// vertices (metadata loads, context transfers, auth) are skipped. A vertex's
// first line is its name; later lines with the same number are its logs and
// statuses and never count.
func (t *StepTracker) Observe(line string) (string, bool) {
	if t == nil {
		return "", false
	}
	id, rest, ok := vertexLine(strings.TrimRight(line, "\r\n"))
	if !ok {
		return "", false
	}
	if _, seen := t.seen[id]; seen {
		return "", false
	}
	t.seen[id] = struct{}{}
	if t.maxSteps > 0 && t.emitted >= t.maxSteps {
		return "", false
	}
	name := printable(rest)
	if name == "" || setupVertex(name) || statusLine(name) {
		return "", false
	}
	t.emitted++
	return truncate(name, t.maxNameBytes), true
}

// setupVertex reports BuildKit's bookkeeping vertices, which run on every
// build and say nothing about the customer's own steps.
func setupVertex(name string) bool {
	for _, prefix := range []string{
		"[internal]", "[auth]", "load build definition", "load .dockerignore",
		"load metadata for", "local context", "resolve image config",
		"transferring ", "importing cache manifest", "building with ",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// statusLine reports a vertex line that is a status or log, not a name. A
// stream that starts mid-build (a log-ring gap) can see one of these first
// for a vertex; the stored tail of a production build starts with
// "#21 extracting sha256:…" (retail-store, 2026-11-19).
func statusLine(name string) bool {
	if r := name[0]; r >= '0' && r <= '9' { // "0.512 npm warn …": step output
		return true
	}
	for _, prefix := range []string{
		"DONE", "CACHED", "ERROR", "CANCELED", "sha256:", "extracting ", "writing layer",
		"writing manifest", "writing config", "preparing ", "sending ", "exporting layers",
		"exporting manifest", "exporting config", "naming to ", "unpacking ", "resolve ",
		"...", "WARN", "DEPRECATED",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// printable drops control characters, so a step name a customer wrote cannot
// move a terminal's cursor or inject escapes into a progress line.
func printable(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

// LatestStep returns the newest step name among build-log lines, oldest
// first, or "" when none is a step line.
func LatestStep(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if name, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), StepLogPrefix); ok && name != "" {
			return printable(name)
		}
	}
	return ""
}
