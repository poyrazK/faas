from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_automatic_evaluation_reason import (
    WorkflowRecoveryAutomaticEvaluationReason,
    check_workflow_recovery_automatic_evaluation_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryAutomaticEvaluation")


@_attrs_define
class WorkflowRecoveryAutomaticEvaluation:
    """Last committed worker evaluation, never refreshed by reads or notification capture. Waiting_since is the first
    observation of this consecutive wait reason and resets when the reason changes. Absent for legacy or unevaluated
    intents. Pausing preserves historical evidence.

    """

    evaluated_at: datetime.datetime
    reason: WorkflowRecoveryAutomaticEvaluationReason
    waiting_since: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        evaluated_at = self.evaluated_at.isoformat()

        reason: str = self.reason

        waiting_since: str | Unset = UNSET
        if not isinstance(self.waiting_since, Unset):
            waiting_since = self.waiting_since.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "evaluated_at": evaluated_at,
                "reason": reason,
            }
        )
        if waiting_since is not UNSET:
            field_dict["waiting_since"] = waiting_since

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        reason = check_workflow_recovery_automatic_evaluation_reason(d.pop("reason"))

        _waiting_since = d.pop("waiting_since", UNSET)
        waiting_since: datetime.datetime | Unset
        if isinstance(_waiting_since, Unset):
            waiting_since = UNSET
        else:
            waiting_since = datetime.datetime.fromisoformat(_waiting_since)

        workflow_recovery_automatic_evaluation = cls(
            evaluated_at=evaluated_at,
            reason=reason,
            waiting_since=waiting_since,
        )

        workflow_recovery_automatic_evaluation.additional_properties = d
        return workflow_recovery_automatic_evaluation

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
