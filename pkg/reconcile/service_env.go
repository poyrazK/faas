package reconcile

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

const (
	serviceEnvPrefix = "GREGALE_SERVICE_"
	serviceEnvSuffix = "_URL"
	serviceEnvPort   = 10080
)

func serviceEnvForWorkloadWithAvailable(base map[string]string, w reposcan.Workload, available map[string]struct{}) map[string]string {
	env := make(map[string]string, len(base)+len(w.DependsOn))
	for key, value := range base {
		if strings.HasPrefix(key, serviceEnvPrefix) && strings.HasSuffix(key, serviceEnvSuffix) {
			continue
		}
		env[key] = value
	}
	for _, dep := range w.DependsOn {
		name := strings.TrimSpace(dep)
		if name == "" {
			continue
		}
		if available != nil {
			if _, ok := available[strings.ToLower(name)]; !ok {
				continue
			}
		}
		env[serviceEnvKey(name)] = fmt.Sprintf("http://%s.svc.gregale:%d", name, serviceEnvPort)
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func serviceEnvKey(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	b.Grow(len(serviceEnvPrefix) + len(name) + len(serviceEnvSuffix))
	b.WriteString(serviceEnvPrefix)
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString(serviceEnvSuffix)
	return b.String()
}

func serviceEnvEqual(actual, expected map[string]string) bool {
	for key, value := range actual {
		if strings.HasPrefix(key, serviceEnvPrefix) && strings.HasSuffix(key, serviceEnvSuffix) {
			if expected[key] != value {
				return false
			}
		}
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}
