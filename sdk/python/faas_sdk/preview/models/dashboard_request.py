from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.dashboard_widget import DashboardWidget


T = TypeVar("T", bound="DashboardRequest")


@_attrs_define
class DashboardRequest:
    """Body of a dashboard create or replace."""

    name: str
    widgets: list[DashboardWidget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        widgets = []
        for widgets_item_data in self.widgets:
            widgets_item = widgets_item_data.to_dict()
            widgets.append(widgets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "widgets": widgets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dashboard_widget import DashboardWidget

        d = dict(src_dict)
        name = d.pop("name")

        widgets = []
        _widgets = d.pop("widgets")
        for widgets_item_data in _widgets:
            widgets_item = DashboardWidget.from_dict(widgets_item_data)

            widgets.append(widgets_item)

        dashboard_request = cls(
            name=name,
            widgets=widgets,
        )

        dashboard_request.additional_properties = d
        return dashboard_request

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
