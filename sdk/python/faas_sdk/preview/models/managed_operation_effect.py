from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ManagedOperationEffect")


@_attrs_define
class ManagedOperationEffect:
    """Named business webhook delivery proposed by a managed HTTP operation handler."""

    name: str
    """Unique within this operation completion."""
    webhook_id: UUID
    """Explicit operation.effect subscriber owned by the authenticated operation scope."""
    type_: str
    """Business event type carried inside the operation.effect payload."""
    payload: Any
    """JSON business data bounded to 64 KiB."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        webhook_id = str(self.webhook_id)

        type_ = self.type_

        payload = self.payload

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "webhook_id": webhook_id,
                "type": type_,
                "payload": payload,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        webhook_id = UUID(d.pop("webhook_id"))

        type_ = d.pop("type")

        payload = d.pop("payload")

        managed_operation_effect = cls(
            name=name,
            webhook_id=webhook_id,
            type_=type_,
            payload=payload,
        )

        return managed_operation_effect
