from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateEdgeRuleListRequest")


@_attrs_define
class UpdateEdgeRuleListRequest:
    description: str | Unset = UNSET
    items: list[str] | Unset = UNSET
    """Replace every item. Cannot be combined with add or remove."""
    add: list[str] | Unset = UNSET
    remove: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        description = self.description

        items: list[str] | Unset = UNSET
        if not isinstance(self.items, Unset):
            items = self.items

        add: list[str] | Unset = UNSET
        if not isinstance(self.add, Unset):
            add = self.add

        remove: list[str] | Unset = UNSET
        if not isinstance(self.remove, Unset):
            remove = self.remove

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if description is not UNSET:
            field_dict["description"] = description
        if items is not UNSET:
            field_dict["items"] = items
        if add is not UNSET:
            field_dict["add"] = add
        if remove is not UNSET:
            field_dict["remove"] = remove

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        description = d.pop("description", UNSET)

        items = cast(list[str], d.pop("items", UNSET))

        add = cast(list[str], d.pop("add", UNSET))

        remove = cast(list[str], d.pop("remove", UNSET))

        update_edge_rule_list_request = cls(
            description=description,
            items=items,
            add=add,
            remove=remove,
        )

        update_edge_rule_list_request.additional_properties = d
        return update_edge_rule_list_request

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
