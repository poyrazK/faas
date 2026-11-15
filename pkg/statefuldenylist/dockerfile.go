package statefuldenylist

import (
	"strings"
)

// FinalStageBase returns the base image of a Dockerfile's final stage, with
// stage aliases resolved (`FROM redis AS base` … `FROM base` → `redis`). It
// returns "" when the Dockerfile has no FROM, or when the base is built from a
// build argument (`FROM ${BASE}`) and so can't be known before the build.
//
// Only the final stage is the image that runs; an earlier stage built FROM a
// datastore image (to copy a client binary out, say) is fine.
func FinalStageBase(dockerfile []byte) string {
	aliases := map[string]string{}
	final := ""
	for _, line := range logicalLines(string(dockerfile)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		args := fields[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			args = args[1:] // --platform=…
		}
		if len(args) == 0 {
			continue
		}
		image := args[0]
		if resolved, ok := aliases[strings.ToLower(image)]; ok {
			image = resolved
		}
		if strings.Contains(image, "$") {
			image = ""
		}
		if len(args) >= 3 && strings.EqualFold(args[1], "AS") {
			aliases[strings.ToLower(args[2])] = image
		}
		final = image
	}
	return final
}

// MatchDockerfile reports whether a Dockerfile's final stage runs a
// datastore image. It returns that base image and the managed-service hint.
func MatchDockerfile(dockerfile []byte) (base, hint string, denied bool) {
	base = FinalStageBase(dockerfile)
	if base == "" || strings.EqualFold(base, "scratch") {
		return "", "", false
	}
	hint, denied = Match(base)
	if !denied {
		return "", "", false
	}
	return base, hint, true
}

// logicalLines joins backslash-continued lines and drops comments.
func logicalLines(text string) []string {
	var out []string
	var current strings.Builder
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if current.Len() == 0 && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		if strings.HasSuffix(line, `\`) {
			current.WriteString(strings.TrimSuffix(line, `\`))
			current.WriteByte(' ')
			continue
		}
		current.WriteString(line)
		out = append(out, current.String())
		current.Reset()
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}
