from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_evidence_category import (
    OperationWorkflowReadinessEvidenceCategory,
    check_operation_workflow_readiness_evidence_category,
)
from ..models.operation_workflow_readiness_evidence_status import (
    OperationWorkflowReadinessEvidenceStatus,
    check_operation_workflow_readiness_evidence_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessEvidence")


@_attrs_define
class OperationWorkflowReadinessEvidence:
    """Compact app-reported evidence observation preserving prerequisite group/path or verification obligation identity."""

    category: OperationWorkflowReadinessEvidenceCategory
    """Evidence category."""
    key: str
    """Stable scoped evidence identity digest."""
    name: str
    """Human-readable requirement or blocker identity."""
    status: OperationWorkflowReadinessEvidenceStatus
    """Retained observation status."""
    fingerprint: str
    """Stable evidence content digest excluding moving age counters."""
    owner: str | Unset = UNSET
    """Reported evidence owner."""
    operation_id: UUID | Unset = UNSET
    """Operation containing the reported evidence or expected verification."""
    invalidation_operation_id: UUID | Unset = UNSET
    """Operation containing the invalidating milestone."""

    def to_dict(self) -> dict[str, Any]:
        category: str = self.category

        key = self.key

        name = self.name

        status: str = self.status

        fingerprint = self.fingerprint

        owner = self.owner

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        invalidation_operation_id: str | Unset = UNSET
        if not isinstance(self.invalidation_operation_id, Unset):
            invalidation_operation_id = str(self.invalidation_operation_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "category": category,
                "key": key,
                "name": name,
                "status": status,
                "fingerprint": fingerprint,
            }
        )
        if owner is not UNSET:
            field_dict["owner"] = owner
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if invalidation_operation_id is not UNSET:
            field_dict["invalidation_operation_id"] = invalidation_operation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        category = check_operation_workflow_readiness_evidence_category(d.pop("category"))

        key = d.pop("key")

        name = d.pop("name")

        status = check_operation_workflow_readiness_evidence_status(d.pop("status"))

        fingerprint = d.pop("fingerprint")

        owner = d.pop("owner", UNSET)

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        _invalidation_operation_id = d.pop("invalidation_operation_id", UNSET)
        invalidation_operation_id: UUID | Unset
        if isinstance(_invalidation_operation_id, Unset):
            invalidation_operation_id = UNSET
        else:
            invalidation_operation_id = UUID(_invalidation_operation_id)

        operation_workflow_readiness_evidence = cls(
            category=category,
            key=key,
            name=name,
            status=status,
            fingerprint=fingerprint,
            owner=owner,
            operation_id=operation_id,
            invalidation_operation_id=invalidation_operation_id,
        )

        return operation_workflow_readiness_evidence
