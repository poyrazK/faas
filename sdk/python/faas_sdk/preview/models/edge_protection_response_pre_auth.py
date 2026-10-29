from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EdgeProtectionResponsePreAuth")


@_attrs_define
class EdgeProtectionResponsePreAuth:
    blocked: int
    """Requests the pre-auth source limit rejected in enforce mode."""
    would_block: int
    """Requests observe mode would have rejected."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        blocked = self.blocked

        would_block = self.would_block

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "blocked": blocked,
                "would_block": would_block,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        blocked = d.pop("blocked")

        would_block = d.pop("would_block")

        edge_protection_response_pre_auth = cls(
            blocked=blocked,
            would_block=would_block,
        )

        edge_protection_response_pre_auth.additional_properties = d
        return edge_protection_response_pre_auth

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
