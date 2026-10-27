from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperationExecutionControlResponse")


@_attrs_define
class OperationExecutionControlResponse:
    """Current HTTP attempt observation. Contains no input, result, owner or capability; grants no renewal."""

    operation_id: UUID
    invocation_id: UUID
    attempt: int
    cancellation_requested: bool
    deadline_at: datetime.datetime
    lease_expires_at: datetime.datetime
    """Current claim expiry, capped by deadline_at. Only the scheduler can renew it."""
    observed_at: datetime.datetime
    """Server observation time. Derive a conservative local duration and subtract request latency."""
    poll_after_ms: int
    """Recommended polling interval; clients also stop at the earlier lease or deadline bound."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        invocation_id = str(self.invocation_id)

        attempt = self.attempt

        cancellation_requested = self.cancellation_requested

        deadline_at = self.deadline_at.isoformat()

        lease_expires_at = self.lease_expires_at.isoformat()

        observed_at = self.observed_at.isoformat()

        poll_after_ms = self.poll_after_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "invocation_id": invocation_id,
                "attempt": attempt,
                "cancellation_requested": cancellation_requested,
                "deadline_at": deadline_at,
                "lease_expires_at": lease_expires_at,
                "observed_at": observed_at,
                "poll_after_ms": poll_after_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        invocation_id = UUID(d.pop("invocation_id"))

        attempt = d.pop("attempt")

        cancellation_requested = d.pop("cancellation_requested")

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        lease_expires_at = datetime.datetime.fromisoformat(d.pop("lease_expires_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        poll_after_ms = d.pop("poll_after_ms")

        operation_execution_control_response = cls(
            operation_id=operation_id,
            invocation_id=invocation_id,
            attempt=attempt,
            cancellation_requested=cancellation_requested,
            deadline_at=deadline_at,
            lease_expires_at=lease_expires_at,
            observed_at=observed_at,
            poll_after_ms=poll_after_ms,
        )

        operation_execution_control_response.additional_properties = d
        return operation_execution_control_response

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
