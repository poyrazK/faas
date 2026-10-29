from typing import Literal

OperationWorkflowPlaybookEvidenceRequirementKind = Literal["milestone", "prerequisite", "verification"]

OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_REQUIREMENT_KIND_VALUES: set[OperationWorkflowPlaybookEvidenceRequirementKind] = {
    "milestone",
    "prerequisite",
    "verification",
}


def check_operation_workflow_playbook_evidence_requirement_kind(
    value: str,
) -> OperationWorkflowPlaybookEvidenceRequirementKind:
    if value in OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_REQUIREMENT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PLAYBOOK_EVIDENCE_REQUIREMENT_KIND_VALUES!r}"
    )
