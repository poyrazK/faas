from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryPolicyAutomaticExecution")


@_attrs_define
class WorkflowRecoveryPolicyAutomaticExecution:
    """Durable consecutive failure counter and automatic execution pause for the current policy version. Updating the
    policy explicitly clears this pause.

    """

    consecutive_failures: int
    pause_reason: str
    paused_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consecutive_failures = self.consecutive_failures

        pause_reason = self.pause_reason

        paused_at: str | Unset = UNSET
        if not isinstance(self.paused_at, Unset):
            paused_at = self.paused_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consecutive_failures": consecutive_failures,
                "pause_reason": pause_reason,
            }
        )
        if paused_at is not UNSET:
            field_dict["paused_at"] = paused_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consecutive_failures = d.pop("consecutive_failures")

        pause_reason = d.pop("pause_reason")

        _paused_at = d.pop("paused_at", UNSET)
        paused_at: datetime.datetime | Unset
        if isinstance(_paused_at, Unset):
            paused_at = UNSET
        else:
            paused_at = datetime.datetime.fromisoformat(_paused_at)

        workflow_recovery_policy_automatic_execution = cls(
            consecutive_failures=consecutive_failures,
            pause_reason=pause_reason,
            paused_at=paused_at,
        )

        workflow_recovery_policy_automatic_execution.additional_properties = d
        return workflow_recovery_policy_automatic_execution

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
