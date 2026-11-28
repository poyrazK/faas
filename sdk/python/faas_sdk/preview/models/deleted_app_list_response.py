from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deleted_app_summary import DeletedAppSummary


T = TypeVar("T", bound="DeletedAppListResponse")


@_attrs_define
class DeletedAppListResponse:
    """Bounded account-owned tombstone inventory and its opaque cursor."""

    account_id: UUID
    apps: list[DeletedAppSummary]
    evaluated_at: datetime.datetime
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        apps = []
        for apps_item_data in self.apps:
            apps_item = apps_item_data.to_dict()
            apps.append(apps_item)

        evaluated_at = self.evaluated_at.isoformat()

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "apps": apps,
                "evaluated_at": evaluated_at,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deleted_app_summary import DeletedAppSummary

        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        apps = []
        _apps = d.pop("apps")
        for apps_item_data in _apps:
            apps_item = DeletedAppSummary.from_dict(apps_item_data)

            apps.append(apps_item)

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        next_cursor = d.pop("next_cursor", UNSET)

        deleted_app_list_response = cls(
            account_id=account_id,
            apps=apps,
            evaluated_at=evaluated_at,
            next_cursor=next_cursor,
        )

        deleted_app_list_response.additional_properties = d
        return deleted_app_list_response

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
