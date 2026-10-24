from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EdgeProtectionResponsePenalty")


@_attrs_define
class EdgeProtectionResponsePenalty:
    """Penalty boxes (ADR-20261022105846); refusals also appear in rejections under gate penalty."""

    boxed: int
    """Clients a rule's penalty boxed."""
    rejected: int
    """Requests refused from boxed clients."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        boxed = self.boxed

        rejected = self.rejected

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "boxed": boxed,
                "rejected": rejected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        boxed = d.pop("boxed")

        rejected = d.pop("rejected")

        edge_protection_response_penalty = cls(
            boxed=boxed,
            rejected=rejected,
        )

        edge_protection_response_penalty.additional_properties = d
        return edge_protection_response_penalty

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
