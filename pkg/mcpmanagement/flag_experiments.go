package mcpmanagement

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Flag experiment reads (ADR 20261116190208 §3): an agent reads the same
// results and recommendation the CLI prints, without parsing statistics.
// Changes go through gregale_plan_experiment_change (ADR-20261119190000),
// and a person approves production starts and ships.

type flagExperimentsArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
}

type flagExperimentArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
	Key         string `json:"key" jsonschema:"Experiment key"`
}

const flagExperimentGuidance = "Report recommendation.action and recommendation.summary as the decision, and recommendation.commands as the owner's next step; ship or stop only through gregale_plan_experiment_change once the owner agrees. Results are always-valid sequential statistics, so reading them often does not inflate false positives. A rolled_back action means a guardrail already sent every customer of the rule to control. Guardrail rates compare per-request server errors and requests slower than control's p95, with customers as units. A bandit (results.bandit) reports each arm's best_probability and expected_loss instead of p-values; results with fixed_horizon true (percentile metrics) have no sequential guarantee, so say so."

func registerFlagExperiments(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_flag_experiments", "List a project environment's flag experiments: flag, rule, control, metric, status and stop reason. Read-only.", m.listFlagExperiments)
	register(s, m, "gregale_get_flag_experiment_results", "Read one flag experiment's sequential results, guardrails (server error rate, p95 latency) and a recommendation with the CLI commands that carry it out. Read-only; Gregale never ships a variant on its own.", m.getFlagExperimentResults)
}

func validateFlagExperimentScope(project, environment string) error {
	if err := validateSegment(project, "project"); err != nil {
		return err
	}
	return validateSegment(environment, "environment")
}

// experimentReadScope checks the environment is inside this connection's
// consent before an experiment read.
func (m *management) experimentReadScope(ctx context.Context, project, environment string) error {
	if err := validateFlagExperimentScope(project, environment); err != nil {
		return err
	}
	_, _, err := m.environmentAccess(ctx, project, environment, false, false)
	return err
}

func (m *management) listFlagExperiments(ctx context.Context, args flagExperimentsArgs) (any, error) {
	if err := m.experimentReadScope(ctx, args.Project, args.Environment); err != nil {
		return nil, err
	}
	list, err := m.client.ListProjectFlagExperiments(ctx, args.Project, args.Environment)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": list.Experiments, "total": len(list.Experiments)}, nil
}

func (m *management) getFlagExperimentResults(ctx context.Context, args flagExperimentArgs) (any, error) {
	if err := m.experimentReadScope(ctx, args.Project, args.Environment); err != nil {
		return nil, err
	}
	if err := validateSegment(args.Key, "key"); err != nil {
		return nil, err
	}
	res, err := m.client.ProjectFlagExperimentResults(ctx, args.Project, args.Environment, args.Key)
	if err != nil {
		return nil, err
	}
	return map[string]any{"results": res, "guidance": flagExperimentGuidance}, nil
}
