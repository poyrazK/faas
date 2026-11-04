package wire

import (
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
)

// partialDeleter is every labelled metric vector (counter, gauge, histogram
// and summary vectors).
type partialDeleter interface {
	DeletePartialMatch(labels prometheus.Labels) int
}

// ForgetApp deletes every series of m labelled with appID under "app" or
// "app_id" and returns how many it deleted (ADR 20261103131546). schedd
// calls it for an app that left its list (deleted, or moved to another
// scheduler): about thirty families carry an app label and none was ever
// deleted, so a long-running schedd kept a series per outcome for every app
// it had ever owned. A family without either label is untouched.
func (m *OpsMetrics) ForgetApp(appID string) int {
	if m == nil || appID == "" {
		return 0
	}
	deleted := 0
	v := reflect.ValueOf(m).Elem()
	for i := range v.NumField() {
		field := v.Field(i)
		if field.Kind() != reflect.Pointer || field.IsNil() {
			continue
		}
		vec, ok := reflect.NewAt(field.Type(), field.Addr().UnsafePointer()).Elem().Interface().(partialDeleter)
		if !ok {
			continue
		}
		deleted += vec.DeletePartialMatch(prometheus.Labels{"app": appID})
		deleted += vec.DeletePartialMatch(prometheus.Labels{"app_id": appID})
	}
	return deleted
}
