package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func verifyCloneValuePublicationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource) error {
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil || len(records) == 0 {
		return err
	}
	scopes := make(map[string]string, len(records))
	targets := make(map[string]projectCloneWorkloadValues, len(records))
	for _, record := range records {
		scopes[record.AppID] = op.TargetEnvironment
		targets[record.AppID] = projectCloneWorkloadValues{}
	}
	rawScopes, err := json.Marshal(scopes)
	if err != nil {
		return err
	}
	variables, secrets, err := projectCloneValuesDB(ctx, tx, ProjectEnvironmentClone{
		AccountID: op.AccountID, ProjectID: op.ProjectID, sourceValueScopesJSON: rawScopes,
	})
	if err != nil {
		return err
	}
	for _, value := range variables {
		target := targets[value.AppID]
		target.Variables = append(target.Variables, value)
		targets[value.AppID] = target
	}
	for _, value := range secrets {
		target := targets[value.AppID]
		target.Secrets = append(target.Secrets, value)
		targets[value.AppID] = target
	}
	return validateCloneValuePublication(op, resources, records, targets)
}
