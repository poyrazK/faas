from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_condition_freshness_status import (
    OperationWorkflowConditionFreshnessStatus,
    check_operation_workflow_condition_freshness_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowConditionFreshness")


@_attrs_define
class OperationWorkflowConditionFreshness:
    """Report-age status using the selected fact reporting Operation's immutable condition definition and the summary
    evaluation time. A positive budget yields a refresh deadline from occurred_at; overdue begins at the deadline.
    Missing or zero budgets remain no_budget. This does not expire facts, change reported results, or authorize
    execution. Republishing an explicit snapshot updates reporting freshness; it does not prove renewed business
    verification.

    """

    status: OperationWorkflowConditionFreshnessStatus
    refresh_owner: str | Unset = UNSET
    """Public refresh owner copied from the selected fact milestone on its reporting Operation pinned definition.
    UTF-8 byte bound; no controls or surrounding whitespace. Omission is unassigned."""
    refresh_instructions: str | Unset = UNSET
    """Application-declared refresh instructions copied from the selected fact pinned condition definition. UTF-8
    byte bound; no controls or surrounding whitespace. Displayed as text and never executed."""
    evidence_max_age_seconds: int | Unset = UNSET
    refresh_due_at: datetime.datetime | Unset = UNSET
    overdue_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        refresh_owner = self.refresh_owner

        refresh_instructions = self.refresh_instructions

        evidence_max_age_seconds = self.evidence_max_age_seconds

        refresh_due_at: str | Unset = UNSET
        if not isinstance(self.refresh_due_at, Unset):
            refresh_due_at = self.refresh_due_at.isoformat()

        overdue_seconds = self.overdue_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
            }
        )
        if refresh_owner is not UNSET:
            field_dict["refresh_owner"] = refresh_owner
        if refresh_instructions is not UNSET:
            field_dict["refresh_instructions"] = refresh_instructions
        if evidence_max_age_seconds is not UNSET:
            field_dict["evidence_max_age_seconds"] = evidence_max_age_seconds
        if refresh_due_at is not UNSET:
            field_dict["refresh_due_at"] = refresh_due_at
        if overdue_seconds is not UNSET:
            field_dict["overdue_seconds"] = overdue_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_operation_workflow_condition_freshness_status(d.pop("status"))

        refresh_owner = d.pop("refresh_owner", UNSET)

        refresh_instructions = d.pop("refresh_instructions", UNSET)

        evidence_max_age_seconds = d.pop("evidence_max_age_seconds", UNSET)

        _refresh_due_at = d.pop("refresh_due_at", UNSET)
        refresh_due_at: datetime.datetime | Unset
        if isinstance(_refresh_due_at, Unset):
            refresh_due_at = UNSET
        else:
            refresh_due_at = datetime.datetime.fromisoformat(_refresh_due_at)

        overdue_seconds = d.pop("overdue_seconds", UNSET)

        operation_workflow_condition_freshness = cls(
            status=status,
            refresh_owner=refresh_owner,
            refresh_instructions=refresh_instructions,
            evidence_max_age_seconds=evidence_max_age_seconds,
            refresh_due_at=refresh_due_at,
            overdue_seconds=overdue_seconds,
        )

        return operation_workflow_condition_freshness
