from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryFailureEligibility")


@_attrs_define
class WorkflowRecoveryFailureEligibility:
    """Latest retained-state retry checks, observed after the original failure. No inputs or outputs are retained; this is
    not failure-time proof or an admission guarantee.

    """

    observed_at: datetime.datetime
    reason: str
    blockers: list[str]
    parent_job_id: UUID | Unset = UNSET
    parent_position: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        reason = self.reason

        blockers = self.blockers

        parent_job_id: str | Unset = UNSET
        if not isinstance(self.parent_job_id, Unset):
            parent_job_id = str(self.parent_job_id)

        parent_position = self.parent_position

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "reason": reason,
                "blockers": blockers,
            }
        )
        if parent_job_id is not UNSET:
            field_dict["parent_job_id"] = parent_job_id
        if parent_position is not UNSET:
            field_dict["parent_position"] = parent_position

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        reason = d.pop("reason")

        blockers = cast(list[str], d.pop("blockers"))

        _parent_job_id = d.pop("parent_job_id", UNSET)
        parent_job_id: UUID | Unset
        if isinstance(_parent_job_id, Unset):
            parent_job_id = UNSET
        else:
            parent_job_id = UUID(_parent_job_id)

        parent_position = d.pop("parent_position", UNSET)

        workflow_recovery_failure_eligibility = cls(
            observed_at=observed_at,
            reason=reason,
            blockers=blockers,
            parent_job_id=parent_job_id,
            parent_position=parent_position,
        )

        workflow_recovery_failure_eligibility.additional_properties = d
        return workflow_recovery_failure_eligibility

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
