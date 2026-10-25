from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountTraceWake")


@_attrs_define
class AccountTraceWake:
    """How a matched request's app was woken, when its wake history is still retained. Omitted for warm requests."""

    wake_id: str
    method: str | Unset = UNSET
    """How the instance started, for example restore or cold_boot."""
    ready_in_ms: int | Unset = UNSET
    """Milliseconds from boot start to ready; omitted until the wake completed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_id = self.wake_id

        method = self.method

        ready_in_ms = self.ready_in_ms

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wake_id = d.pop("wake_id")

        method = d.pop("method", UNSET)

        ready_in_ms = d.pop("ready_in_ms", UNSET)

        account_trace_wake = cls(
            wake_id=wake_id,
            method=method,
            ready_in_ms=ready_in_ms,
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
