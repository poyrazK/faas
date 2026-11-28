from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_support_ticket_request_category import (
    CreateSupportTicketRequestCategory,
    check_create_support_ticket_request_category,
)
from ..models.create_support_ticket_request_priority import (
    CreateSupportTicketRequestPriority,
    check_create_support_ticket_request_priority,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.support_context import SupportContext


T = TypeVar("T", bound="CreateSupportTicketRequest")


@_attrs_define
class CreateSupportTicketRequest:
    """Submit a support request within the authenticated personal or organization scope."""

    subject: str
    category: CreateSupportTicketRequestCategory
    body: str
    priority: CreateSupportTicketRequestPriority | Unset = "normal"
    context: SupportContext | Unset = UNSET
    """Optional identifiers supplied by the customer; no automatic log, secret or source-code collection."""

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject

        category: str = self.category

        body = self.body

        priority: str | Unset = UNSET
        if not isinstance(self.priority, Unset):
            priority = self.priority

        context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.context, Unset):
            context = self.context.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "category": category,
                "body": body,
            }
        )
        if priority is not UNSET:
            field_dict["priority"] = priority
        if context is not UNSET:
            field_dict["context"] = context

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.support_context import SupportContext

        d = dict(src_dict)
        subject = d.pop("subject")

        category = check_create_support_ticket_request_category(d.pop("category"))

        body = d.pop("body")

        _priority = d.pop("priority", UNSET)
        priority: CreateSupportTicketRequestPriority | Unset
        if isinstance(_priority, Unset):
            priority = UNSET
        else:
            priority = check_create_support_ticket_request_priority(_priority)

        _context = d.pop("context", UNSET)
        context: SupportContext | Unset
        if isinstance(_context, Unset):
            context = UNSET
        else:
            context = SupportContext.from_dict(_context)

        create_support_ticket_request = cls(
            subject=subject,
            category=category,
            body=body,
            priority=priority,
            context=context,
        )

        return create_support_ticket_request
