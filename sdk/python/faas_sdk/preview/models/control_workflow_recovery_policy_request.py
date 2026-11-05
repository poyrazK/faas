from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.control_workflow_recovery_policy_request_operation import (
    ControlWorkflowRecoveryPolicyRequestOperation,
    check_control_workflow_recovery_policy_request_operation,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ControlWorkflowRecoveryPolicyRequest")


@_attrs_define
class ControlWorkflowRecoveryPolicyRequest:
    """Operational pause or resume using the last observed policy and pause state. Omit expected_paused_at when the
    observed policy was unpaused. Every accepted control updates updated_at and records a trusted audit actor and reason
    without changing the policy version. A retry using stale state returns conflict.

    """

    expected_version: int
    expected_updated_at: datetime.datetime
    operation: ControlWorkflowRecoveryPolicyRequestOperation
    reason: str
    expected_paused_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        expected_updated_at = self.expected_updated_at.isoformat()

        operation: str = self.operation

        reason = self.reason

        expected_paused_at: str | Unset = UNSET
        if not isinstance(self.expected_paused_at, Unset):
            expected_paused_at = self.expected_paused_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_version": expected_version,
                "expected_updated_at": expected_updated_at,
                "operation": operation,
                "reason": reason,
            }
        )
        if expected_paused_at is not UNSET:
            field_dict["expected_paused_at"] = expected_paused_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        expected_updated_at = datetime.datetime.fromisoformat(d.pop("expected_updated_at"))

        operation = check_control_workflow_recovery_policy_request_operation(d.pop("operation"))

        reason = d.pop("reason")

        _expected_paused_at = d.pop("expected_paused_at", UNSET)
        expected_paused_at: datetime.datetime | Unset
        if isinstance(_expected_paused_at, Unset):
            expected_paused_at = UNSET
        else:
            expected_paused_at = datetime.datetime.fromisoformat(_expected_paused_at)

        control_workflow_recovery_policy_request = cls(
            expected_version=expected_version,
            expected_updated_at=expected_updated_at,
            operation=operation,
            reason=reason,
            expected_paused_at=expected_paused_at,
        )

        control_workflow_recovery_policy_request.additional_properties = d
        return control_workflow_recovery_policy_request

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
