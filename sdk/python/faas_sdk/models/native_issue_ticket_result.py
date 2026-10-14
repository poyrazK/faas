from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.native_issue_ticket_result_state import (
    NativeIssueTicketResultState,
    check_native_issue_ticket_result_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="NativeIssueTicketResult")


@_attrs_define
class NativeIssueTicketResult:
    """Durable GitHub ticket evidence and Gregale attachment state for a native creation operation."""

    request_id: UUID
    state: NativeIssueTicketResultState
    ticket_url: str
    link_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        state: str = self.state

        ticket_url = self.ticket_url

        link_id: str | Unset = UNSET
        if not isinstance(self.link_id, Unset):
            link_id = str(self.link_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "state": state,
                "ticket_url": ticket_url,
            }
        )
        if link_id is not UNSET:
            field_dict["link_id"] = link_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        state = check_native_issue_ticket_result_state(d.pop("state"))

        ticket_url = d.pop("ticket_url")

        _link_id = d.pop("link_id", UNSET)
        link_id: UUID | Unset
        if isinstance(_link_id, Unset):
            link_id = UNSET
        else:
            link_id = UUID(_link_id)

        native_issue_ticket_result = cls(
            request_id=request_id,
            state=state,
            ticket_url=ticket_url,
            link_id=link_id,
        )

        native_issue_ticket_result.additional_properties = d
        return native_issue_ticket_result

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
