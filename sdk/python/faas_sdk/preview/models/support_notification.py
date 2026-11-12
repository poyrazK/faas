from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.support_notification_kind import SupportNotificationKind, check_support_notification_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="SupportNotification")


@_attrs_define
class SupportNotification:
    """An actor-specific support update containing metadata only."""

    id: UUID
    sequence: int
    ticket_id: UUID
    kind: SupportNotificationKind
    created_at: datetime.datetime
    read_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        sequence = self.sequence

        ticket_id = str(self.ticket_id)

        kind: str = self.kind

        created_at = self.created_at.isoformat()

        read_at: str | Unset = UNSET
        if not isinstance(self.read_at, Unset):
            read_at = self.read_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "sequence": sequence,
                "ticket_id": ticket_id,
                "kind": kind,
                "created_at": created_at,
            }
        )
        if read_at is not UNSET:
            field_dict["read_at"] = read_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        ticket_id = UUID(d.pop("ticket_id"))

        kind = check_support_notification_kind(d.pop("kind"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _read_at = d.pop("read_at", UNSET)
        read_at: datetime.datetime | Unset
        if isinstance(_read_at, Unset):
            read_at = UNSET
        else:
            read_at = datetime.datetime.fromisoformat(_read_at)

        support_notification = cls(
            id=id,
            sequence=sequence,
            ticket_id=ticket_id,
            kind=kind,
            created_at=created_at,
            read_at=read_at,
        )

        return support_notification
