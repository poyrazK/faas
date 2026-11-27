package meter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The sweeps keep their SQL static; this pins their interval to
// api.RouteResourceRetention so the two cannot drift apart.
func TestRouteResourceRetentionMatchesConstant(t *testing.T) {
	want := fmt.Sprintf("interval '%d days'", int(api.RouteResourceRetention.Hours()/24))
	for name, sql := range map[string]string{
		"instance_resource_minutes": retentionInstanceResourceMinutesBatchSQL,
		"instance_route_minutes":    retentionInstanceRouteMinutesBatchSQL,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("%s sweep does not use %s:\n%s", name, want, sql)
		}
	}
}
