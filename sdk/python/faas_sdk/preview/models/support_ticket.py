from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.support_ticket_status import SupportTicketStatus, check_support_ticket_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.support_context import SupportContext


T = TypeVar("T", bound="SupportTicket")


@_attrs_define
class SupportTicket:
    """Customer-visible case metadata; staff assignments, targets, notes and engineering records stay private."""

    id: UUID
    subject: str
    category: str
    status: SupportTicketStatus
    revision: int
    context: SupportContext
    """Optional identifiers supplied by the customer; no automatic log, secret or source-code collection."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    can_reopen: bool
    resolved_at: datetime.datetime | Unset = UNSET
    reopen_until: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        subject = self.subject

        category = self.category

        status: str = self.status

        revision = self.revision

        context = self.context.to_dict()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        can_reopen = self.can_reopen

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        reopen_until: str | Unset = UNSET
        if not isinstance(self.reopen_until, Unset):
            reopen_until = self.reopen_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "subject": subject,
                "category": category,
                "status": status,
                "revision": revision,
                "context": context,
                "created_at": created_at,
                "updated_at": updated_at,
                "can_reopen": can_reopen,
            }
        )
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if reopen_until is not UNSET:
            field_dict["reopen_until"] = reopen_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.support_context import SupportContext

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        subject = d.pop("subject")

        category = d.pop("category")

        status = check_support_ticket_status(d.pop("status"))

        revision = d.pop("revision")

        context = SupportContext.from_dict(d.pop("context"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        can_reopen = d.pop("can_reopen")

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        _reopen_until = d.pop("reopen_until", UNSET)
        reopen_until: datetime.datetime | Unset
        if isinstance(_reopen_until, Unset):
            reopen_until = UNSET
        else:
            reopen_until = datetime.datetime.fromisoformat(_reopen_until)

        support_ticket = cls(
            id=id,
            subject=subject,
            category=category,
            status=status,
            revision=revision,
            context=context,
            created_at=created_at,
            updated_at=updated_at,
            can_reopen=can_reopen,
            resolved_at=resolved_at,
            reopen_until=reopen_until,
        )

        return support_ticket
