// Package buildprogress reads BuildKit's plain progress output, the text
// buildctl writes with --progress plain, to tell where a build spent its
// time. The builder guest summarizes its own build with it, so builderd can
// report the breakdown without the node's console log.
package buildprogress

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Step is one BuildKit vertex that finished.
type Step struct {
	Name     string
	Duration time.Duration
}

// Summary is where one build's time went.
type Summary struct {
	// Slowest are the longest finished steps, longest first.
	Slowest []Step
	// ImageExport sums the "exporting to ..." steps: the image and any other
	// output. CacheExport sums the "exporting cache ..." steps.
	ImageExport time.Duration
	CacheExport time.Duration
}

// Steps returns the vertices that finished in out, in the order BuildKit
// first printed them. A vertex's first line is its name. BuildKit prints the
// name again whenever the vertex's output resumes, and its logs and statuses
// follow it. Cached, failed and cancelled vertices report no duration and are
// left out.
func Steps(out []byte) []Step {
	names := map[int]string{}
	var order []int
	done := map[int]time.Duration{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		id, rest, ok := vertexLine(string(bytes.TrimRight(line, "\r")))
		if !ok {
			continue
		}
		if _, seen := names[id]; !seen {
			names[id] = rest
			order = append(order, id)
			continue
		}
		if d, ok := doneDuration(rest); ok {
			done[id] = d
		}
	}
	steps := make([]Step, 0, len(done))
	for _, id := range order {
		if d, ok := done[id]; ok {
			steps = append(steps, Step{Name: names[id], Duration: d})
		}
	}
	return steps
}

// Summarize returns at most maxSteps of the slowest steps, with names cut to
// maxNameBytes, and the export totals.
func Summarize(out []byte, maxSteps, maxNameBytes int) Summary {
	steps := Steps(out)
	var s Summary
	for _, step := range steps {
		switch {
		case strings.HasPrefix(step.Name, "exporting cache"):
			s.CacheExport += step.Duration
		case strings.HasPrefix(step.Name, "exporting to "):
			s.ImageExport += step.Duration
		}
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Duration > steps[j].Duration })
	if len(steps) > maxSteps {
		steps = steps[:max(maxSteps, 0)]
	}
	for i := range steps {
		steps[i].Name = truncate(steps[i].Name, maxNameBytes)
	}
	if len(steps) > 0 {
		s.Slowest = steps
	}
	return s
}

// vertexLine splits "#12 rest" into 12 and "rest".
func vertexLine(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	number, rest, found := strings.Cut(line[1:], " ")
	if !found || rest == "" {
		return 0, "", false
	}
	id, err := strconv.Atoi(number)
	if err != nil || id < 0 {
		return 0, "", false
	}
	return id, rest, true
}

// doneDuration reads "DONE 12.5s".
func doneDuration(rest string) (time.Duration, bool) {
	value, found := strings.CutPrefix(rest, "DONE ")
	if !found {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(value, "s"), 64)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
