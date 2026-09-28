from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.org_app_deployment_summary import OrgAppDeploymentSummary


T = TypeVar("T", bound="OrgAppDeploymentListResponse")


@_attrs_define
class OrgAppDeploymentListResponse:
    """Newest-first paginated safe deployment history for one workspace app."""

    items: list[OrgAppDeploymentSummary]
    next_before: datetime.datetime | Unset = UNSET
    """Cursor for the next older page; omitted when there are no more rows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_before: str | Unset = UNSET
        if not isinstance(self.next_before, Unset):
            next_before = self.next_before.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.org_app_deployment_summary import OrgAppDeploymentSummary

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OrgAppDeploymentSummary.from_dict(items_item_data)

            items.append(items_item)

        _next_before = d.pop("next_before", UNSET)
        next_before: datetime.datetime | Unset
        if isinstance(_next_before, Unset):
            next_before = UNSET
        else:
            next_before = datetime.datetime.fromisoformat(_next_before)

        org_app_deployment_list_response = cls(
            items=items,
            next_before=next_before,
        )

        org_app_deployment_list_response.additional_properties = d
        return org_app_deployment_list_response

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
