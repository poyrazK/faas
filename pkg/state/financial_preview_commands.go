package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type FinancialPreviewCommandRuntime struct {
	TaskID, LeaseToken, AccountID, AppID, DeploymentID, NodeID, EnvironmentID, Phase string
	RAMMB, CPUMillicores                                                             int
	RecordedAt                                                                       time.Time
	PrepareUntil                                                                     time.Time
}

type FinancialPreviewCommandStore interface {
	RegisterFinancialPreviewCommand(context.Context, FinancialPreviewCommandRuntime) error
	ActivateFinancialPreviewCommand(context.Context, string, string) error
	RetireFinancialPreviewCommand(context.Context, string, string, time.Time) error
	FinancialPreviewCommands(context.Context, string, string) ([]FinancialPreviewCommandRuntime, error)
}

func validFinancialPreviewCommand(r FinancialPreviewCommandRuntime) bool {
	for _, id := range []string{r.TaskID, r.LeaseToken, r.AccountID, r.AppID, r.DeploymentID} {
		if uuid.Validate(id) != nil {
			return false
		}
	}
	return r.NodeID != "" && r.RAMMB > 0 && r.RAMMB <= api.FinancialBudgetResidentCountMax && r.CPUMillicores > 0 && r.CPUMillicores <= api.FinancialBudgetResidentCountMax && !r.RecordedAt.IsZero() && !r.PrepareUntil.Before(r.RecordedAt) &&
		(r.EnvironmentID == "" || uuid.Validate(r.EnvironmentID) == nil)
}
