from typing import Literal

WorkflowRecoveryPolicyEvaluationCandidateEvidenceSource = Literal["failure_inventory", "policy_review"]

WORKFLOW_RECOVERY_POLICY_EVALUATION_CANDIDATE_EVIDENCE_SOURCE_VALUES: set[
    WorkflowRecoveryPolicyEvaluationCandidateEvidenceSource
] = {
    "failure_inventory",
    "policy_review",
}


def check_workflow_recovery_policy_evaluation_candidate_evidence_source(
    value: str,
) -> WorkflowRecoveryPolicyEvaluationCandidateEvidenceSource:
    if value in WORKFLOW_RECOVERY_POLICY_EVALUATION_CANDIDATE_EVIDENCE_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_POLICY_EVALUATION_CANDIDATE_EVIDENCE_SOURCE_VALUES!r}"
    )
