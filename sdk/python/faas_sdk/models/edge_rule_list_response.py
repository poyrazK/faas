from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_list_response_kind import EdgeRuleListResponseKind, check_edge_rule_list_response_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleListResponse")


@_attrs_define
class EdgeRuleListResponse:
    """A reusable account-level list (ADR-833)."""

    id: str
    name: str
    kind: EdgeRuleListResponseKind
    item_count: int
    referenced_by: list[str]
    """IDs of rules whose match conditions use the list."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    description: str | Unset = UNSET
    items: list[str] | Unset = UNSET
    """Present when one list is fetched."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        name = self.name

        kind: str = self.kind

        item_count = self.item_count

        referenced_by = self.referenced_by

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        description = self.description

        items: list[str] | Unset = UNSET
        if not isinstance(self.items, Unset):
            items = self.items

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "kind": kind,
                "item_count": item_count,
                "referenced_by": referenced_by,
                "created_at": created_at,
                "updated_at": updated_at,
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
        id = d.pop("id")

        name = d.pop("name")

        kind = check_edge_rule_list_response_kind(d.pop("kind"))

        item_count = d.pop("item_count")

        referenced_by = cast(list[str], d.pop("referenced_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        description = d.pop("description", UNSET)

        items = cast(list[str], d.pop("items", UNSET))

        edge_rule_list_response = cls(
            id=id,
            name=name,
            kind=kind,
            item_count=item_count,
            referenced_by=referenced_by,
            created_at=created_at,
            updated_at=updated_at,
            description=description,
            items=items,
        )

        edge_rule_list_response.additional_properties = d
        return edge_rule_list_response

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
