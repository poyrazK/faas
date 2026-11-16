from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.support_message_author import SupportMessageAuthor, check_support_message_author

T = TypeVar("T", bound="SupportMessage")


@_attrs_define
class SupportMessage:
    """One public conversation message, with a marker in place of redacted content."""

    id: UUID
    sequence: int
    author: SupportMessageAuthor
    body: str
    redacted: bool
    created_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        sequence = self.sequence

        author: str = self.author

        body = self.body

        redacted = self.redacted

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "sequence": sequence,
                "author": author,
                "body": body,
                "redacted": redacted,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        sequence = d.pop("sequence")

        author = check_support_message_author(d.pop("author"))

        body = d.pop("body")

        redacted = d.pop("redacted")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        support_message = cls(
            id=id,
            sequence=sequence,
            author=author,
            body=body,
            redacted=redacted,
            created_at=created_at,
        )

        return support_message
