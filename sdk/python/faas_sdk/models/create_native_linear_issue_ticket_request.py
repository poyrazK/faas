from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateNativeLinearIssueTicketRequest")


@_attrs_define
class CreateNativeLinearIssueTicketRequest:
    """Explicit creation or UUID recovery through the app owner's connected Linear team."""

    request_id: UUID
    """Stable UUID v4 used as the Linear issue ID and reused across retries."""
    team_id: UUID
    """Verified team UUID matching the stored app connection."""
    title: str
    """Explicit ticket title bounded to 256 UTF-8 bytes."""
    description: str | Unset = UNSET
    """Optional description bounded to 16 KiB; no telemetry is copied automatically."""
    recover: bool | Unset = False
    """Explicitly recover an unconfirmed dispatch by its stable request UUID after the dispatch lease expires."""
    recovery_url: str | Unset = UNSET
    """Optional known Linear issue URL that also requests recovery and must match the UUID lookup."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        team_id = str(self.team_id)

        title = self.title

        description = self.description

        recover = self.recover

        recovery_url = self.recovery_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "team_id": team_id,
                "title": title,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description
        if recover is not UNSET:
            field_dict["recover"] = recover
        if recovery_url is not UNSET:
            field_dict["recovery_url"] = recovery_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        team_id = UUID(d.pop("team_id"))

        title = d.pop("title")

        description = d.pop("description", UNSET)

        recover = d.pop("recover", UNSET)

        recovery_url = d.pop("recovery_url", UNSET)

        create_native_linear_issue_ticket_request = cls(
            request_id=request_id,
            team_id=team_id,
            title=title,
            description=description,
            recover=recover,
            recovery_url=recovery_url,
        )

        create_native_linear_issue_ticket_request.additional_properties = d
        return create_native_linear_issue_ticket_request

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
