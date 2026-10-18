package meter

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func meterAppSubject(app state.App, plans map[string]api.Plan) string {
	if _, shared := plans[app.OrgID]; shared {
		return app.OrgID
	}
	return app.AccountID
}
