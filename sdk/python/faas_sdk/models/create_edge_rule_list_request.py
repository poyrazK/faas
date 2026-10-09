from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_edge_rule_list_request_kind import (
    CreateEdgeRuleListRequestKind,
    check_create_edge_rule_list_request_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateEdgeRuleListRequest")


@_attrs_define
class CreateEdgeRuleListRequest:
    name: str
    kind: CreateEdgeRuleListRequestKind
    """ip (addresses and CIDRs, for client_ip), country (ISO 3166-1 alpha-2), host (exact hosts and *.suffix
    patterns), string (exact values, for path, header, cookie and query fields)."""
    description: str | Unset = UNSET
    items: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind: str = self.kind

        description = self.description

        items: list[str] | Unset = UNSET
        if not isinstance(self.items, Unset):
            items = self.items

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "kind": kind,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description
        if items is not UNSET:
            field_dict["items"] = items

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        kind = check_create_edge_rule_list_request_kind(d.pop("kind"))

        description = d.pop("description", UNSET)

        items = cast(list[str], d.pop("items", UNSET))

        create_edge_rule_list_request = cls(
            name=name,
            kind=kind,
            description=description,
            items=items,
        )

        create_edge_rule_list_request.additional_properties = d
        return create_edge_rule_list_request

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
