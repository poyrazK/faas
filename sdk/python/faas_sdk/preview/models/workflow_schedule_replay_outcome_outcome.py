from typing import Literal

WorkflowScheduleReplayOutcomeOutcome = Literal[
    "already_replayed",
    "definition_changed",
    "deployment_changed",
    "eligible",
    "history_not_replayable",
    "not_skipped",
    "occurrence_not_found",
    "overlap_active",
    "plan_unavailable",
    "quota_full",
    "replayed",
    "schedule_disabled",
    "target_unavailable",
    "tenant_unavailable",
]

WORKFLOW_SCHEDULE_REPLAY_OUTCOME_OUTCOME_VALUES: set[WorkflowScheduleReplayOutcomeOutcome] = {
    "already_replayed",
    "definition_changed",
    "deployment_changed",
    "eligible",
    "history_not_replayable",
    "not_skipped",
    "occurrence_not_found",
    "overlap_active",
    "plan_unavailable",
    "quota_full",
    "replayed",
    "schedule_disabled",
    "target_unavailable",
    "tenant_unavailable",
}


def check_workflow_schedule_replay_outcome_outcome(value: str) -> WorkflowScheduleReplayOutcomeOutcome:
    if value in WORKFLOW_SCHEDULE_REPLAY_OUTCOME_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_REPLAY_OUTCOME_OUTCOME_VALUES!r}")
