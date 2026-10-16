from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueTeam")


@_attrs_define
class IssueTeam:
    """Application-scoped team identity, display name, complete member roster and editor metadata."""

    id: UUID
    key: str
    name: str
    member_account_ids: list[UUID]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    updated_by_account_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        key = self.key

        name = self.name

        member_account_ids = []
        for member_account_ids_item_data in self.member_account_ids:
            member_account_ids_item = str(member_account_ids_item_data)
            member_account_ids.append(member_account_ids_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        updated_by_account_id: str | Unset = UNSET
        if not isinstance(self.updated_by_account_id, Unset):
            updated_by_account_id = str(self.updated_by_account_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "key": key,
                "name": name,
                "member_account_ids": member_account_ids,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if updated_by_account_id is not UNSET:
            field_dict["updated_by_account_id"] = updated_by_account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        key = d.pop("key")

        name = d.pop("name")

        member_account_ids = []
        _member_account_ids = d.pop("member_account_ids")
        for member_account_ids_item_data in _member_account_ids:
            member_account_ids_item = UUID(member_account_ids_item_data)

            member_account_ids.append(member_account_ids_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _updated_by_account_id = d.pop("updated_by_account_id", UNSET)
        updated_by_account_id: UUID | Unset
        if isinstance(_updated_by_account_id, Unset):
            updated_by_account_id = UNSET
        else:
            updated_by_account_id = UUID(_updated_by_account_id)

        issue_team = cls(
            id=id,
            key=key,
            name=name,
            member_account_ids=member_account_ids,
            created_at=created_at,
            updated_at=updated_at,
            updated_by_account_id=updated_by_account_id,
        )

        issue_team.additional_properties = d
        return issue_team

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
