package reposcan

import "strings"

// composeProfiles is the set of Compose profiles a scan deploys
// (ADR-20261022190000). Compose activates profiles with COMPOSE_PROFILES or
// --profile; Gregale has neither a shell nor flags, so it reads
// COMPOSE_PROFILES from the source's .env, where Compose reads it too.
type composeProfiles struct {
	all   bool
	names map[string]struct{}
}

// composeActiveProfiles parses COMPOSE_PROFILES: comma-separated names, with
// "*" enabling every profile, as in Compose.
func composeActiveProfiles(env map[string]string) composeProfiles {
	active := composeProfiles{names: map[string]struct{}{}}
	for _, raw := range strings.Split(env["COMPOSE_PROFILES"], ",") {
		switch name := strings.TrimSpace(raw); name {
		case "":
		case "*":
			active.all = true
		default:
			active.names[name] = struct{}{}
		}
	}
	return active
}

// enables reports whether a service with these profiles is deployed: any one
// active profile is enough, and a service without profiles always is.
func (p composeProfiles) enables(profiles []string) bool {
	if len(profiles) == 0 || p.all {
		return true
	}
	for _, profile := range profiles {
		if _, ok := p.names[strings.TrimSpace(profile)]; ok {
			return true
		}
	}
	return false
}
