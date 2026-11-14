from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SupportSubmission")


@_attrs_define
class SupportSubmission:
    """Durable transaction receipt; UUID idempotency keys replay indefinitely while current scope access remains valid."""

    ticket_id: UUID
    submission_id: UUID
    revision: int

    def to_dict(self) -> dict[str, Any]:
        ticket_id = str(self.ticket_id)

        submission_id = str(self.submission_id)

        revision = self.revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "ticket_id": ticket_id,
                "submission_id": submission_id,
                "revision": revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ticket_id = UUID(d.pop("ticket_id"))

        submission_id = UUID(d.pop("submission_id"))

        revision = d.pop("revision")

        support_submission = cls(
            ticket_id=ticket_id,
            submission_id=submission_id,
            revision=revision,
        )

        return support_submission
