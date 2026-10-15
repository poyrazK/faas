from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutOperationWebhookBindingRequest")


@_attrs_define
class PutOperationWebhookBindingRequest:
    """Bind a verified provider endpoint to a customer operation with revision control (ADR-1193)."""

    expected_version: int
    operation: str
    """Operation name resolved on the scope's live deployment for each event."""
    platform_tenant_id: UUID
    """Operation owner fixed by this binding."""
    take_over_delivery: bool
    scope: str | Unset = UNSET
    """Environment scope; defaults to default."""
    event_types: list[str] | Unset = UNSET
    """Exact event types to admit; empty admits every verified event."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        operation = self.operation

        platform_tenant_id = str(self.platform_tenant_id)

        take_over_delivery = self.take_over_delivery

        scope = self.scope

        event_types: list[str] | Unset = UNSET
        if not isinstance(self.event_types, Unset):
            event_types = self.event_types

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "operation": operation,
                "platform_tenant_id": platform_tenant_id,
                "take_over_delivery": take_over_delivery,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if event_types is not UNSET:
            field_dict["event_types"] = event_types

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        operation = d.pop("operation")

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        take_over_delivery = d.pop("take_over_delivery")

        scope = d.pop("scope", UNSET)

        event_types = cast(list[str], d.pop("event_types", UNSET))

        put_operation_webhook_binding_request = cls(
            expected_version=expected_version,
            operation=operation,
            platform_tenant_id=platform_tenant_id,
            take_over_delivery=take_over_delivery,
            scope=scope,
            event_types=event_types,
        )

        return put_operation_webhook_binding_request
