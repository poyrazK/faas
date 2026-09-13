from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.public_status_update_state import PublicStatusUpdateState, check_public_status_update_state

T = TypeVar("T", bound="PublicStatusUpdate")


@_attrs_define
class PublicStatusUpdate:
    """One append-only, plain-text event timeline entry."""

    id: UUID
    state: PublicStatusUpdateState
    message: str
    posted_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        state: str = self.state

        message = self.message

        posted_at = self.posted_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "state": state,
                "message": message,
                "posted_at": posted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        state = check_public_status_update_state(d.pop("state"))

        message = d.pop("message")

        posted_at = datetime.datetime.fromisoformat(d.pop("posted_at"))

        public_status_update = cls(
            id=id,
            state=state,
            message=message,
            posted_at=posted_at,
        )

        return public_status_update
