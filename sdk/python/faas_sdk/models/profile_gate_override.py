from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProfileGateOverride")


@_attrs_define
class ProfileGateOverride:
    expected_policy_revision: int
    reason: str
    """Nonblank customer reason recorded atomically with the traffic change. Workers cannot override."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_policy_revision = self.expected_policy_revision

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_policy_revision": expected_policy_revision,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_policy_revision = d.pop("expected_policy_revision")

        reason = d.pop("reason")

        profile_gate_override = cls(
            expected_policy_revision=expected_policy_revision,
            reason=reason,
        )

        profile_gate_override.additional_properties = d
        return profile_gate_override

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
