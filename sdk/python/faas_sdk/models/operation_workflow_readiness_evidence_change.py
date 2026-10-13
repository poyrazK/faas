from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_evidence_change_kind import (
    OperationWorkflowReadinessEvidenceChangeKind,
    check_operation_workflow_readiness_evidence_change_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_evidence import OperationWorkflowReadinessEvidence


T = TypeVar("T", bound="OperationWorkflowReadinessEvidenceChange")


@_attrs_define
class OperationWorkflowReadinessEvidenceChange:
    """Individual comparison; absence is unavailable unless complete inventories establish blocker clearance."""

    kind: OperationWorkflowReadinessEvidenceChangeKind
    """Evidence change classification."""
    before: OperationWorkflowReadinessEvidence | Unset = UNSET
    """Compact app-reported evidence observation preserving prerequisite group/path or verification obligation
    identity."""
    after: OperationWorkflowReadinessEvidence | Unset = UNSET
    """Compact app-reported evidence observation preserving prerequisite group/path or verification obligation
    identity."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_evidence import OperationWorkflowReadinessEvidence

        d = dict(src_dict)
        kind = check_operation_workflow_readiness_evidence_change_kind(d.pop("kind"))

        _before = d.pop("before", UNSET)
        before: OperationWorkflowReadinessEvidence | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = OperationWorkflowReadinessEvidence.from_dict(_before)

        _after = d.pop("after", UNSET)
        after: OperationWorkflowReadinessEvidence | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = OperationWorkflowReadinessEvidence.from_dict(_after)

        operation_workflow_readiness_evidence_change = cls(
            kind=kind,
            before=before,
            after=after,
        )

        return operation_workflow_readiness_evidence_change
