from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreatePreviewRequest")


@_attrs_define
class CreatePreviewRequest:
    """Pull-request preview provisioning payload. Source is deployed separately after the preview app is created."""

    pr_number: int
    """Pull-request number used to derive the stable preview slug pr-{N}-{parent_slug}."""
    ttl_hours: int | Unset = 168
    """Preview lease duration. The server defaults to 168 hours and caps it at 30 days."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pr_number = self.pr_number

        ttl_hours = self.ttl_hours

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pr_number": pr_number,
            }
        )
        if ttl_hours is not UNSET:
            field_dict["ttl_hours"] = ttl_hours

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pr_number = d.pop("pr_number")

        ttl_hours = d.pop("ttl_hours", UNSET)

        create_preview_request = cls(
            pr_number=pr_number,
            ttl_hours=ttl_hours,
        )

        create_preview_request.additional_properties = d
        return create_preview_request

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
