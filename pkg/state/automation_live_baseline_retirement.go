// adr: 1305
package state

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type AutomationLiveBaselineRetirementStore interface {
	RetireAutomationLiveBaseline(context.Context, string, string, string, string, string, string, api.RetireAutomationLiveBaselineRequest) (api.AutomationLiveBaselineRetirement, error)
}

func baselineRetiredError() error {
	return api.NewProblem(http.StatusGone, api.CodeConflict, "Baseline retired", "This baseline name has been retired permanently. Select an active baseline explicitly; promotion history remains available.")
}
func baselineLifetimeLimitError(observed int64) error {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Baseline audit name limit reached", "This automation has reached its lifetime baseline-name limit. Reuse an active name.").WithLimit(api.AutomationLiveBaselineLifetimeNameLimit, observed).WithDocs("https://gregale.dev/docs/automations#baselines")
}
func validateBaselineRetirement(alias string, values []string, revision int64) error {
	if !api.ValidAutomationLiveBaselineName(alias) || revision <= 0 || revision == math.MaxInt64 {
		return api.ErrValidation("Provide a valid baseline name, retirement UUID and positive expected revision.")
	}
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return api.ErrValidation("Provide valid retirement and actor UUIDs.")
		}
	}
	return nil
}
func retirementReplay(receipt api.AutomationLiveBaselineRetirement, appID, name, alias, id, actor, key string, revision int64) bool {
	return receipt.AppID == appID && receipt.Name == name && receipt.BaselineName == alias && receipt.RetirementID == id && receipt.ActorAccountID == actor && receipt.ActorAPIKeyID == key && receipt.Revision == revision+1
}
func retiredBaseline(prior api.AutomationLiveBaseline, id, actor, key string) api.AutomationLiveBaselineRetirement {
	return api.AutomationLiveBaselineRetirement{RetirementID: id, AppID: prior.AppID, Name: prior.Name, BaselineName: prior.BaselineName, Revision: prior.Revision + 1, PreviousPromotionID: prior.PromotionID, ActorAccountID: actor, ActorAPIKeyID: key, RetiredAt: time.Now().UTC()}
}
func retirementIdentityValues(id, actor, key string) []string {
	values := []string{id, actor}
	if key != "" {
		values = append(values, key)
	}
	return values
}
