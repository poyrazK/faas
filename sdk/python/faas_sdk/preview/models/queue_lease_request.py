from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueLeaseRequest")


@_attrs_define
class QueueLeaseRequest:
    """Pull lease. Zero values lease one message for the default visibility timeout (30s) and return at once."""

    max_messages: int | Unset = UNSET
    """A lease also stops once its payloads and headers would pass 8 MiB; it always returns at least one ready
    message."""
    visibility_timeout_seconds: int | Unset = UNSET
    """At most the plan's async invocation deadline."""
    wait_seconds: int | Unset = UNSET
    """When no message is ready, hold the call until one arrives or this many seconds pass, then return an empty
    list."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_messages = self.max_messages

        visibility_timeout_seconds = self.visibility_timeout_seconds

        wait_seconds = self.wait_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if max_messages is not UNSET:
            field_dict["max_messages"] = max_messages
        if visibility_timeout_seconds is not UNSET:
            field_dict["visibility_timeout_seconds"] = visibility_timeout_seconds
        if wait_seconds is not UNSET:
            field_dict["wait_seconds"] = wait_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_messages = d.pop("max_messages", UNSET)

        visibility_timeout_seconds = d.pop("visibility_timeout_seconds", UNSET)

        wait_seconds = d.pop("wait_seconds", UNSET)

        queue_lease_request = cls(
            max_messages=max_messages,
            visibility_timeout_seconds=visibility_timeout_seconds,
            wait_seconds=wait_seconds,
        )

        queue_lease_request.additional_properties = d
        return queue_lease_request

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
