from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_trace_wake_restore_fallback_reason import (
    AccountTraceWakeRestoreFallbackReason,
    check_account_trace_wake_restore_fallback_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountTraceWake")


@_attrs_define
class AccountTraceWake:
    """How a matched request's app was woken, when its wake history is still retained. Omitted for warm requests."""

    wake_id: str
    method: str | Unset = UNSET
    """How the instance started, for example restore or cold_boot. A planned restore that fell back to a cold boot
    reads cold_boot."""
    ready_in_ms: int | Unset = UNSET
    """Milliseconds from boot start to ready; omitted until the wake completed."""
    planned_method: str | Unset = UNSET
    """The method schedd planned when vmmd ran another one, for example restore on a wake that fell back to a cold
    boot. Absent otherwise."""
    restore_fallback_reason: AccountTraceWakeRestoreFallbackReason | Unset = UNSET
    """Closed reason for a restore that fell back to a cold boot, when one is reported."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_id = self.wake_id

        method = self.method

        ready_in_ms = self.ready_in_ms

        planned_method = self.planned_method

        restore_fallback_reason: str | Unset = UNSET
        if not isinstance(self.restore_fallback_reason, Unset):
            restore_fallback_reason = self.restore_fallback_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "wake_id": wake_id,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if ready_in_ms is not UNSET:
            field_dict["ready_in_ms"] = ready_in_ms
        if planned_method is not UNSET:
            field_dict["planned_method"] = planned_method
        if restore_fallback_reason is not UNSET:
            field_dict["restore_fallback_reason"] = restore_fallback_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wake_id = d.pop("wake_id")

        method = d.pop("method", UNSET)

        ready_in_ms = d.pop("ready_in_ms", UNSET)

        planned_method = d.pop("planned_method", UNSET)

        _restore_fallback_reason = d.pop("restore_fallback_reason", UNSET)
        restore_fallback_reason: AccountTraceWakeRestoreFallbackReason | Unset
        if isinstance(_restore_fallback_reason, Unset):
            restore_fallback_reason = UNSET
        else:
            restore_fallback_reason = check_account_trace_wake_restore_fallback_reason(_restore_fallback_reason)

        account_trace_wake = cls(
            wake_id=wake_id,
            method=method,
            ready_in_ms=ready_in_ms,
            planned_method=planned_method,
            restore_fallback_reason=restore_fallback_reason,
        )

        account_trace_wake.additional_properties = d
        return account_trace_wake

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
