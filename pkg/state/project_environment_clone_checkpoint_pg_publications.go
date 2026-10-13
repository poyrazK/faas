package state

import "context"

// The publication journal owns the entire original PostgreSQL binding matrix.
// It does not make a checkpoint database customer-visible or publish a release.
type ProjectEnvironmentCloneCheckpointPgPublicationPlan struct {
	OperationID, ActivationPlanSHA256, ActivationObservationSHA256 string
	Secrets                                                        []AppSecret
}
type ProjectEnvironmentCloneCheckpointPgPublication struct {
	Plan              ProjectEnvironmentCloneCheckpointPgPublicationPlan
	State, PlanSHA256 string
}
type ProjectEnvironmentCloneCheckpointPgPublicationInputs struct {
	Activation  ProjectEnvironmentCloneCheckpointPgActivation
	Connections []ProjectEnvironmentCloneCheckpointPgConnectionValue
	Credentials []ProjectEnvironmentCloneCheckpointPostgresCredential
	Publication *ProjectEnvironmentCloneCheckpointPgPublication
	// Committed, authenticated workload-value preparations, indexed by the
	// original source binding. These do not establish resource publication proof.
	Preparations map[string]ProjectEnvironmentClonePostgresBindingPreparation
	BindingGraph *ProjectEnvironmentCloneCheckpointPgBindingGraph
}
type ProjectEnvironmentCloneCheckpointPgPublicationStore interface {
	ProjectEnvironmentCloneCheckpointPgPublicationInputs(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointPgPublicationInputs, error)
	PublishProjectEnvironmentCloneCheckpointPgBindings(context.Context, ProjectEnvironmentCloneLease, []AppSecret, ProjectEnvironmentCloneCheckpointPgActivationObservation, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPgPublication, error)
	WithdrawProjectEnvironmentCloneCheckpointPgBindings(context.Context, ProjectEnvironmentCloneLease) error
}
