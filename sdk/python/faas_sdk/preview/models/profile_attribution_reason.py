from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_attribution_reason_reason import (
    ProfileAttributionReasonReason,
    check_profile_attribution_reason_reason,
)

T = TypeVar("T", bound="ProfileAttributionReason")


@_attrs_define
class ProfileAttributionReason:
    """Bounded evidence explaining why profile attribution is incomplete or inconsistent."""

    reason: ProfileAttributionReasonReason
    cpu_seconds: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        cpu_seconds = self.cpu_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "cpu_seconds": cpu_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_profile_attribution_reason_reason(d.pop("reason"))

        cpu_seconds = d.pop("cpu_seconds")

        profile_attribution_reason = cls(
            reason=reason,
            cpu_seconds=cpu_seconds,
        )

        profile_attribution_reason.additional_properties = d
        return profile_attribution_reason

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
