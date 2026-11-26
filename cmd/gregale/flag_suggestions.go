package main

import (
	"flag"
	"strings"
)

// Consult the actual parser, including flags registered after its writer was
// installed. Suggestions never change arguments or apply to value errors.
// flagSynonyms maps a flag name people bring from other tools to the flags
// that may mean the same here, most likely first. Only a candidate the
// command defines is suggested. `invoke --data` (curl's spelling) got a bare
// "unknown flag" because no edit-distance match exists (prod hunts r25, r27).
var flagSynonyms = map[string][]string{
	"data":  {"payload", "body"},
	"body":  {"payload", "data"},
	"input": {"payload"},
	// `gregale app` shows "idle timeout" and "min instances" (prod hunt r36).
	"idle-timeout":  {"idle"},
	"min-instances": {"min"},
	"max-instances": {"max", "max-concurrency"},
}

func suggestUnknownFlag(diagnostic string, fs *flag.FlagSet) string {
	name, ok := strings.CutPrefix(diagnostic, "unknown flag --")
	if ok && fs != nil {
		bare, _, _ := strings.Cut(name, "=")
		for _, candidate := range flagSynonyms[bare] {
			if fs.Lookup(candidate) != nil {
				return candidate
			}
		}
	}
	if !ok || fs == nil || len(name) < 4 || len(name) > 64 {
		return ""
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return ""
		}
	}
	limit := 1
	if len(name) > 6 {
		limit = 2
	}
	best, tied, suggestion := limit+1, false, ""
	fs.VisitAll(func(candidate *flag.Flag) {
		if len(candidate.Name) < 4 || len(candidate.Name) > 64 {
			return
		}
		distance := editDistance(name, candidate.Name)
		if distance < best {
			best, tied, suggestion = distance, false, candidate.Name
		} else if distance == best {
			tied = true
		}
	})
	if best > limit || tied {
		return ""
	}
	return suggestion
}
