package projectsetup

import (
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

// ManagedConsumer is a service that expects a Compose datastore's address in
// one environment key (ADR-20261025111824).
type ManagedConsumer struct {
	Workload string
	EnvKey   string
}

// ManagedConsumers lists the selected services that use the Compose
// datastore service: every key whose Compose value points at it (for example
// DATABASE_URL: postgres://app:secret@db:5432/app), and, for a service that
// depends_on it without such a key and without its own value for the
// datastore's conventional key (hint), that key.
// The result is sorted by workload, then key.
func ManagedConsumers(env reposcan.ComposeEnv, workloads []reposcan.Workload, service, hint string) []ManagedConsumer {
	service = strings.ToLower(strings.TrimSpace(service))
	var out []ManagedConsumer
	for _, w := range workloads {
		var keys []string
		for key, value := range env.Services[w.Name] {
			if value != nil && valueHost(*value) == service && !PlatformProvidedEnvKey(key) {
				keys = append(keys, key)
			}
		}
		// The conventional key is used only when the service does not set it
		// to something else, such as a database it already runs elsewhere.
		_, setsHint := env.Services[w.Name][hint]
		if len(keys) == 0 && hint != "" && !setsHint && slices.ContainsFunc(w.DependsOn, func(name string) bool {
			return strings.EqualFold(strings.TrimSpace(name), service)
		}) {
			keys = append(keys, hint)
		}
		slices.Sort(keys)
		for _, key := range keys {
			out = append(out, ManagedConsumer{Workload: w.Name, EnvKey: key})
		}
	}
	slices.SortFunc(out, func(a, b ManagedConsumer) int {
		if c := strings.Compare(a.Workload, b.Workload); c != 0 {
			return c
		}
		return strings.Compare(a.EnvKey, b.EnvKey)
	})
	return out
}
