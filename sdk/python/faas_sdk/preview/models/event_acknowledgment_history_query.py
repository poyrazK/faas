from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_acknowledgment_history_query_kind import (
    EventAcknowledgmentHistoryQueryKind,
    check_event_acknowledgment_history_query_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventAcknowledgmentHistoryQuery")


@_attrs_define
class EventAcknowledgmentHistoryQuery:
    """Scope-bound page options selecting coalesced groups by first occurrence."""

    invocation_id: UUID | Unset = UNSET
    """Delivery filter shared by every continuation request."""
    kind: EventAcknowledgmentHistoryQueryKind | Unset = UNSET
    """Outcome kind shared by the page traversal."""
    from_: datetime.datetime | Unset = UNSET
    """Inclusive first_at bound for the traversal."""
    until: datetime.datetime | Unset = UNSET
    """Exclusive first_at bound for the traversal."""
    limit: int | Unset = 100
    """Page size included in the cursor scope binding."""
    cursor: str | Unset = UNSET
    """Previous page continuation retaining its initial cutoff."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        from_: str | Unset = UNSET
        if not isinstance(self.from_, Unset):
            from_ = self.from_.isoformat()

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        limit = self.limit

        cursor = self.cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if kind is not UNSET:
            field_dict["kind"] = kind
        if from_ is not UNSET:
            field_dict["from"] = from_
        if until is not UNSET:
            field_dict["until"] = until
        if limit is not UNSET:
            field_dict["limit"] = limit
        if cursor is not UNSET:
            field_dict["cursor"] = cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        _kind = d.pop("kind", UNSET)
        kind: EventAcknowledgmentHistoryQueryKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_event_acknowledgment_history_query_kind(_kind)

        _from_ = d.pop("from", UNSET)
        from_: datetime.datetime | Unset
        if isinstance(_from_, Unset):
            from_ = UNSET
        else:
            from_ = datetime.datetime.fromisoformat(_from_)

        _until = d.pop("until", UNSET)
        until: datetime.datetime | Unset
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = datetime.datetime.fromisoformat(_until)

        limit = d.pop("limit", UNSET)

        cursor = d.pop("cursor", UNSET)

        event_acknowledgment_history_query = cls(
            invocation_id=invocation_id,
            kind=kind,
            from_=from_,
            until=until,
            limit=limit,
            cursor=cursor,
        )

        event_acknowledgment_history_query.additional_properties = d
        return event_acknowledgment_history_query

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
