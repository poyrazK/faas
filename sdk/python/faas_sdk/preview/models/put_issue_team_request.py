from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PutIssueTeamRequest")


@_attrs_define
class PutIssueTeamRequest:
    """Replacement team display name and eligible account membership roster."""

    name: str
    """Maximum 64 UTF-8 bytes without control characters."""
    member_account_ids: list[UUID]

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        member_account_ids = []
        for member_account_ids_item_data in self.member_account_ids:
            member_account_ids_item = str(member_account_ids_item_data)
            member_account_ids.append(member_account_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "member_account_ids": member_account_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        member_account_ids = []
        _member_account_ids = d.pop("member_account_ids")
        for member_account_ids_item_data in _member_account_ids:
            member_account_ids_item = UUID(member_account_ids_item_data)

            member_account_ids.append(member_account_ids_item)

        put_issue_team_request = cls(
            name=name,
            member_account_ids=member_account_ids,
        )

        return put_issue_team_request
