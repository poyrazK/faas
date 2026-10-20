from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_activity_details import IssueActivityDetails


T = TypeVar("T", bound="IssueActivity")


@_attrs_define
class IssueActivity:
    """An audited issue lifecycle, ownership, or manual regrouping transition."""

    id: UUID
    action: str
    created_at: datetime.datetime
    details: IssueActivityDetails
    original_issue_id: UUID | Unset = UNSET
    actor_account_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        action = self.action

        created_at = self.created_at.isoformat()

        details = self.details.to_dict()

        original_issue_id: str | Unset = UNSET
        if not isinstance(self.original_issue_id, Unset):
            original_issue_id = str(self.original_issue_id)

        actor_account_id: str | Unset = UNSET
        if not isinstance(self.actor_account_id, Unset):
            actor_account_id = str(self.actor_account_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "action": action,
                "created_at": created_at,
                "details": details,
            }
        )
        if original_issue_id is not UNSET:
            field_dict["original_issue_id"] = original_issue_id
        if actor_account_id is not UNSET:
            field_dict["actor_account_id"] = actor_account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_activity_details import IssueActivityDetails

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        action = d.pop("action")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        details = IssueActivityDetails.from_dict(d.pop("details"))

        _original_issue_id = d.pop("original_issue_id", UNSET)
        original_issue_id: UUID | Unset
        if isinstance(_original_issue_id, Unset):
            original_issue_id = UNSET
        else:
            original_issue_id = UUID(_original_issue_id)

        _actor_account_id = d.pop("actor_account_id", UNSET)
        actor_account_id: UUID | Unset
        if isinstance(_actor_account_id, Unset):
            actor_account_id = UNSET
        else:
            actor_account_id = UUID(_actor_account_id)

        issue_activity = cls(
            id=id,
            action=action,
            created_at=created_at,
            details=details,
            original_issue_id=original_issue_id,
            actor_account_id=actor_account_id,
        )

        issue_activity.additional_properties = d
        return issue_activity

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
