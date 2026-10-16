from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_saved_view_filters import IssueSavedViewFilters


T = TypeVar("T", bound="IssueSavedView")


@_attrs_define
class IssueSavedView:
    """Named shared issue query with its stable key and creation and editor metadata."""

    key: str
    name: str
    filters: IssueSavedViewFilters
    """Supported query filters retained by a shared application issue view."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    updated_by_account_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        name = self.name

        filters = self.filters.to_dict()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        updated_by_account_id: str | Unset = UNSET
        if not isinstance(self.updated_by_account_id, Unset):
            updated_by_account_id = str(self.updated_by_account_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "name": name,
                "filters": filters,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if updated_by_account_id is not UNSET:
            field_dict["updated_by_account_id"] = updated_by_account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_saved_view_filters import IssueSavedViewFilters

        d = dict(src_dict)
        key = d.pop("key")

        name = d.pop("name")

        filters = IssueSavedViewFilters.from_dict(d.pop("filters"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _updated_by_account_id = d.pop("updated_by_account_id", UNSET)
        updated_by_account_id: UUID | Unset
        if isinstance(_updated_by_account_id, Unset):
            updated_by_account_id = UNSET
        else:
            updated_by_account_id = UUID(_updated_by_account_id)

        issue_saved_view = cls(
            key=key,
            name=name,
            filters=filters,
            created_at=created_at,
            updated_at=updated_at,
            updated_by_account_id=updated_by_account_id,
        )

        issue_saved_view.additional_properties = d
        return issue_saved_view

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
