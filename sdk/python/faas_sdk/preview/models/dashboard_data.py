from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.dashboard_widget_value import DashboardWidgetValue


T = TypeVar("T", bound="DashboardData")


@_attrs_define
class DashboardData:
    """A dashboard with every widget resolved."""

    id: UUID
    name: str
    as_of: datetime.datetime
    widgets: list[DashboardWidgetValue]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        as_of = self.as_of.isoformat()

        widgets = []
        for widgets_item_data in self.widgets:
            widgets_item = widgets_item_data.to_dict()
            widgets.append(widgets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "as_of": as_of,
                "widgets": widgets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dashboard_widget_value import DashboardWidgetValue

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        widgets = []
        _widgets = d.pop("widgets")
        for widgets_item_data in _widgets:
            widgets_item = DashboardWidgetValue.from_dict(widgets_item_data)

            widgets.append(widgets_item)

        dashboard_data = cls(
            id=id,
            name=name,
            as_of=as_of,
            widgets=widgets,
        )

        dashboard_data.additional_properties = d
        return dashboard_data

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
