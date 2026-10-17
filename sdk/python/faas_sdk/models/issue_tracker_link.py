from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_tracker_link_provider import IssueTrackerLinkProvider, check_issue_tracker_link_provider
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueTrackerLink")


@_attrs_define
class IssueTrackerLink:
    """Stored external ticket reference with provider identity and editor metadata."""

    id: UUID
    original_issue_id: UUID
    """Original identity owning this reference; retained through merge and split."""
    provider: IssueTrackerLinkProvider
    external_key: str
    url: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    title: str | Unset = UNSET
    created_by_account_id: UUID | Unset = UNSET
    updated_by_account_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        original_issue_id = str(self.original_issue_id)

        provider: str = self.provider

        external_key = self.external_key

        url = self.url

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        title = self.title

        created_by_account_id: str | Unset = UNSET
        if not isinstance(self.created_by_account_id, Unset):
            created_by_account_id = str(self.created_by_account_id)

        updated_by_account_id: str | Unset = UNSET
        if not isinstance(self.updated_by_account_id, Unset):
            updated_by_account_id = str(self.updated_by_account_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "original_issue_id": original_issue_id,
                "provider": provider,
                "external_key": external_key,
                "url": url,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if title is not UNSET:
            field_dict["title"] = title
        if created_by_account_id is not UNSET:
            field_dict["created_by_account_id"] = created_by_account_id
        if updated_by_account_id is not UNSET:
            field_dict["updated_by_account_id"] = updated_by_account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        original_issue_id = UUID(d.pop("original_issue_id"))

        provider = check_issue_tracker_link_provider(d.pop("provider"))

        external_key = d.pop("external_key")

        url = d.pop("url")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        title = d.pop("title", UNSET)

        _created_by_account_id = d.pop("created_by_account_id", UNSET)
        created_by_account_id: UUID | Unset
        if isinstance(_created_by_account_id, Unset):
            created_by_account_id = UNSET
        else:
            created_by_account_id = UUID(_created_by_account_id)

        _updated_by_account_id = d.pop("updated_by_account_id", UNSET)
        updated_by_account_id: UUID | Unset
        if isinstance(_updated_by_account_id, Unset):
            updated_by_account_id = UNSET
        else:
            updated_by_account_id = UUID(_updated_by_account_id)

        issue_tracker_link = cls(
            id=id,
            original_issue_id=original_issue_id,
            provider=provider,
            external_key=external_key,
            url=url,
            created_at=created_at,
            updated_at=updated_at,
            title=title,
            created_by_account_id=created_by_account_id,
            updated_by_account_id=updated_by_account_id,
        )

        issue_tracker_link.additional_properties = d
        return issue_tracker_link

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
