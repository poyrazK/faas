from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_evidence_requirement_kind import (
    OperationWorkflowPlaybookEvidenceRequirementKind,
    check_operation_workflow_playbook_evidence_requirement_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookEvidenceRequirement")


@_attrs_define
class OperationWorkflowPlaybookEvidenceRequirement:
    """OperationWorkflowPlaybookEvidenceRequirement. One pinned live application-evidence selector per checklist label.
    Milestone selects a declared operation and milestone. Prerequisite additionally selects an exact current-state
    transition and producer, optionally an alternative group and named path. Verification selects current-report blocker
    resolutions; absent obligations are unavailable.

    """

    label: str
    """OperationWorkflowPlaybookEvidenceRequirement.label. Exact label from required_evidence; unique within
    evidence_requirements."""
    kind: OperationWorkflowPlaybookEvidenceRequirementKind
    """OperationWorkflowPlaybookEvidenceRequirement.kind. Selector category; invalid combinations of fields are
    rejected."""
    operation: str
    """OperationWorkflowPlaybookEvidenceRequirement.operation. Declared milestone producer, prerequisite transition
    operation, or blocker-resolution operation name."""
    milestone: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.milestone. Required milestone name for milestone and
    prerequisite kinds; omitted for verification."""
    prerequisite_operation: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.prerequisite_operation. Required fact producer name for
    prerequisite kind only."""
    from_state: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.from_state. Required exact transition source for prerequisite
    kind only."""
    to_state: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.to_state. Required exact transition destination for
    prerequisite kind only."""
    group_id: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.group_id. Optional prerequisite alternative group. Without a
    group, selects mandatory retained facts."""
    path_id: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.path_id. Optional named prerequisite path; requires group_id.
    Evaluates the selected fact, not the whole path or its decision."""
    blocker_code: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceRequirement.blocker_code. Required blocker code for verification kind only;
    all matching obligations on the current report must be verified."""

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        kind: str = self.kind

        operation = self.operation

        milestone = self.milestone

        prerequisite_operation = self.prerequisite_operation

        from_state = self.from_state

        to_state = self.to_state

        group_id = self.group_id

        path_id = self.path_id

        blocker_code = self.blocker_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "kind": kind,
                "operation": operation,
            }
        )
        if milestone is not UNSET:
            field_dict["milestone"] = milestone
        if prerequisite_operation is not UNSET:
            field_dict["prerequisite_operation"] = prerequisite_operation
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if to_state is not UNSET:
            field_dict["to_state"] = to_state
        if group_id is not UNSET:
            field_dict["group_id"] = group_id
        if path_id is not UNSET:
            field_dict["path_id"] = path_id
        if blocker_code is not UNSET:
            field_dict["blocker_code"] = blocker_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        kind = check_operation_workflow_playbook_evidence_requirement_kind(d.pop("kind"))

        operation = d.pop("operation")

        milestone = d.pop("milestone", UNSET)

        prerequisite_operation = d.pop("prerequisite_operation", UNSET)

        from_state = d.pop("from_state", UNSET)

        to_state = d.pop("to_state", UNSET)

        group_id = d.pop("group_id", UNSET)

        path_id = d.pop("path_id", UNSET)

        blocker_code = d.pop("blocker_code", UNSET)

        operation_workflow_playbook_evidence_requirement = cls(
            label=label,
            kind=kind,
            operation=operation,
            milestone=milestone,
            prerequisite_operation=prerequisite_operation,
            from_state=from_state,
            to_state=to_state,
            group_id=group_id,
            path_id=path_id,
            blocker_code=blocker_code,
        )

        return operation_workflow_playbook_evidence_requirement
