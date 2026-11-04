from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateNativeIssueTicketRequest")


@_attrs_define
class CreateNativeIssueTicketRequest:
    """Explicit content and stable operation identity for native GitHub ticket creation or recovery."""

    request_id: UUID
    repository: str
    title: str
    description: str | Unset = UNSET
    recovery_url: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        repository = self.repository

        title = self.title

        description = self.description

        recovery_url = self.recovery_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "repository": repository,
                "title": title,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description
        if recovery_url is not UNSET:
            field_dict["recovery_url"] = recovery_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        repository = d.pop("repository")

        title = d.pop("title")

        description = d.pop("description", UNSET)

        recovery_url = d.pop("recovery_url", UNSET)

        create_native_issue_ticket_request = cls(
            request_id=request_id,
            repository=repository,
            title=title,
            description=description,
            recovery_url=recovery_url,
        )

        return create_native_issue_ticket_request
