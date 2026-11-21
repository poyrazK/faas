from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_protection_response_state import AppProtectionResponseState, check_app_protection_response_state

if TYPE_CHECKING:
    from ..models.app_protection_item_status import AppProtectionItemStatus


T = TypeVar("T", bound="AppProtectionResponse")


@_attrs_define
class AppProtectionResponse:
    """An app's production protection."""

    state: AppProtectionResponseState
    """on when the app is enrolled, off when its owner turned protection off, none when the app was never enrolled."""
    protected: bool
    """True when the app is enrolled and every protection object is active."""
    items: list[AppProtectionItemStatus]
    """Every protection object in a fixed order; empty when state is none."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        protected = self.protected

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "protected": protected,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_protection_item_status import AppProtectionItemStatus

        d = dict(src_dict)
        state = check_app_protection_response_state(d.pop("state"))

        protected = d.pop("protected")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = AppProtectionItemStatus.from_dict(items_item_data)

            items.append(items_item)

        app_protection_response = cls(
            state=state,
            protected=protected,
            items=items,
        )

        app_protection_response.additional_properties = d
        return app_protection_response

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
