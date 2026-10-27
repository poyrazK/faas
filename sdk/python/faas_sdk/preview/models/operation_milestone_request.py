from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationMilestoneRequest")


@_attrs_define
class OperationMilestoneRequest:
    """Public application fact with a stable UUID reused across retries and recovery. Payloads are validated against the
    immutable definition; publication does not change business outcome.

    """

    id: UUID
    name: str
    payload: Any
    """Declared JSON payload, bounded to 8192 UTF-8 bytes before and after canonicalization. The application
    chooses customer-visible data."""
    occurred_at: datetime.datetime
    """Application-recorded time. Normalized to UTC microsecond precision; independent of platform receipt time."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        payload = self.payload

        occurred_at = self.occurred_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "payload": payload,
                "occurred_at": occurred_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        payload = d.pop("payload")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        operation_milestone_request = cls(
            id=id,
            name=name,
            payload=payload,
            occurred_at=occurred_at,
        )

        return operation_milestone_request
