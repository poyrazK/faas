from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_grant_record import ToolGrantRecord


T = TypeVar("T", bound="ToolGrantPage")


@_attrs_define
class ToolGrantPage:
    """Bounded owner-only grant page ordered by grant UUID."""

    grants: list[ToolGrantRecord]
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grants = []
        for grants_item_data in self.grants:
            grants_item = grants_item_data.to_dict()
            grants.append(grants_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grants": grants,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_grant_record import ToolGrantRecord

        d = dict(src_dict)
        grants = []
        _grants = d.pop("grants")
        for grants_item_data in _grants:
            grants_item = ToolGrantRecord.from_dict(grants_item_data)

            grants.append(grants_item)

        next_cursor = d.pop("next_cursor", UNSET)

        tool_grant_page = cls(
            grants=grants,
            next_cursor=next_cursor,
        )

        tool_grant_page.additional_properties = d
        return tool_grant_page

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
