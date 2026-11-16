from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_protection_item_status_item import AppProtectionItemStatusItem, check_app_protection_item_status_item
from ..models.app_protection_item_status_kind import AppProtectionItemStatusKind, check_app_protection_item_status_kind
from ..models.app_protection_item_status_state import (
    AppProtectionItemStatusState,
    check_app_protection_item_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppProtectionItemStatus")


@_attrs_define
class AppProtectionItemStatus:
    """One production protection object."""

    item: AppProtectionItemStatusItem
    kind: AppProtectionItemStatusKind
    name: str
    state: AppProtectionItemStatusState
    """active when the object was created; pending while the app waits for its first healthy deploy."""
    object_id: str | Unset = UNSET
    """The created object's id, or a release gate's name. Present when state is active."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        item: str = self.item

        kind: str = self.kind

        name = self.name

        state: str = self.state

        object_id = self.object_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "item": item,
                "kind": kind,
                "name": name,
                "state": state,
            }
        )
        if object_id is not UNSET:
            field_dict["object_id"] = object_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        item = check_app_protection_item_status_item(d.pop("item"))

        kind = check_app_protection_item_status_kind(d.pop("kind"))

        name = d.pop("name")

        state = check_app_protection_item_status_state(d.pop("state"))

        object_id = d.pop("object_id", UNSET)

        app_protection_item_status = cls(
            item=item,
            kind=kind,
            name=name,
            state=state,
            object_id=object_id,
        )

        app_protection_item_status.additional_properties = d
        return app_protection_item_status

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
