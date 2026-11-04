from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_resource_inventory_response_kind import (
    AppResourceInventoryResponseKind,
    check_app_resource_inventory_response_kind,
)
from ..models.app_resource_inventory_response_schema_version import (
    AppResourceInventoryResponseSchemaVersion,
    check_app_resource_inventory_response_schema_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_resource_inventory_item import AppResourceInventoryItem


T = TypeVar("T", bound="AppResourceInventoryResponse")


@_attrs_define
class AppResourceInventoryResponse:
    """One native app and scope metadata page with a filtered total and optional next offset."""

    schema_version: AppResourceInventoryResponseSchemaVersion
    app_id: str
    scope: str
    kind: AppResourceInventoryResponseKind
    limit: int
    offset: int
    total: int
    items: list[AppResourceInventoryItem]
    has_more: bool
    generated_at: datetime.datetime
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    next_offset: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema_version: int = self.schema_version

        app_id = self.app_id

        scope = self.scope

        kind: str = self.kind

        limit = self.limit

        offset = self.offset

        total = self.total

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        has_more = self.has_more

        generated_at = self.generated_at.isoformat()

        project_id = self.project_id

        environment_id = self.environment_id

        next_offset = self.next_offset

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema_version": schema_version,
                "app_id": app_id,
                "scope": scope,
                "kind": kind,
                "limit": limit,
                "offset": offset,
                "total": total,
                "items": items,
                "has_more": has_more,
                "generated_at": generated_at,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if next_offset is not UNSET:
            field_dict["next_offset"] = next_offset

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_resource_inventory_item import AppResourceInventoryItem

        d = dict(src_dict)
        schema_version = check_app_resource_inventory_response_schema_version(d.pop("schema_version"))

        app_id = d.pop("app_id")

        scope = d.pop("scope")

        kind = check_app_resource_inventory_response_kind(d.pop("kind"))

        limit = d.pop("limit")

        offset = d.pop("offset")

        total = d.pop("total")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = AppResourceInventoryItem.from_dict(items_item_data)

            items.append(items_item)

        has_more = d.pop("has_more")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        next_offset = d.pop("next_offset", UNSET)

        app_resource_inventory_response = cls(
            schema_version=schema_version,
            app_id=app_id,
            scope=scope,
            kind=kind,
            limit=limit,
            offset=offset,
            total=total,
            items=items,
            has_more=has_more,
            generated_at=generated_at,
            project_id=project_id,
            environment_id=environment_id,
            next_offset=next_offset,
        )

        app_resource_inventory_response.additional_properties = d
        return app_resource_inventory_response

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
