from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessHandoff")


@_attrs_define
class OperationWorkflowReadinessHandoff:
    """Advisory handoff context for the same checked workflow observation; scheduling-time evidence is not retained."""

    summary: str
    """OperationWorkflowReadinessHandoff.summary. Explanation of the current observation or unavailable evidence."""
    comparable: bool
    """OperationWorkflowReadinessHandoff.comparable. Whether current requirements can be shown without crossing a
    comparison boundary."""
    limitations: list[str]
    """OperationWorkflowReadinessHandoff.limitations. Explicit retained-evidence and advisory authority
    limitations."""
    baseline_evaluated_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessHandoff.baseline_evaluated_at. Original notification comparison observation time."""

    def to_dict(self) -> dict[str, Any]:
        summary = self.summary

        comparable = self.comparable

        limitations = self.limitations

        baseline_evaluated_at: str | Unset = UNSET
        if not isinstance(self.baseline_evaluated_at, Unset):
            baseline_evaluated_at = self.baseline_evaluated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "summary": summary,
                "comparable": comparable,
                "limitations": limitations,
            }
        )
        if baseline_evaluated_at is not UNSET:
            field_dict["baseline_evaluated_at"] = baseline_evaluated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        summary = d.pop("summary")

        comparable = d.pop("comparable")

        limitations = cast(list[str], d.pop("limitations"))

        _baseline_evaluated_at = d.pop("baseline_evaluated_at", UNSET)
        baseline_evaluated_at: datetime.datetime | Unset
        if isinstance(_baseline_evaluated_at, Unset):
            baseline_evaluated_at = UNSET
        else:
            baseline_evaluated_at = datetime.datetime.fromisoformat(_baseline_evaluated_at)

        operation_workflow_readiness_handoff = cls(
            summary=summary,
            comparable=comparable,
            limitations=limitations,
            baseline_evaluated_at=baseline_evaluated_at,
        )

        return operation_workflow_readiness_handoff
