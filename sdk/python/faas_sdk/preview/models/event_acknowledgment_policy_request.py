from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EventAcknowledgmentPolicyRequest")


@_attrs_define
class EventAcknowledgmentPolicyRequest:
    """Explicit replacement of a subscription policy for future acceptances."""

    lease_seconds: int
    """Requested processing lease duration; zero disables deferred acknowledgment for future acceptances."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        lease_seconds = self.lease_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "lease_seconds": lease_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        lease_seconds = d.pop("lease_seconds")

        event_acknowledgment_policy_request = cls(
            lease_seconds=lease_seconds,
        )

        event_acknowledgment_policy_request.additional_properties = d
        return event_acknowledgment_policy_request

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
