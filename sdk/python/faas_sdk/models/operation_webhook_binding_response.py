from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperationWebhookBindingResponse")


@_attrs_define
class OperationWebhookBindingResponse:
    """Bound operation, owner, event selection and current endpoint binding revision."""

    endpoint_id: UUID
    app_id: UUID
    scope: str
    operation: str
    platform_tenant_id: UUID
    event_types: list[str]
    version: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        app_id = str(self.app_id)

        scope = self.scope

        operation = self.operation

        platform_tenant_id = str(self.platform_tenant_id)

        event_types = self.event_types

        version = self.version

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "app_id": app_id,
                "scope": scope,
                "operation": operation,
                "platform_tenant_id": platform_tenant_id,
                "event_types": event_types,
                "version": version,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint_id = UUID(d.pop("endpoint_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        operation = d.pop("operation")

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        event_types = cast(list[str], d.pop("event_types"))

        version = d.pop("version")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        operation_webhook_binding_response = cls(
            endpoint_id=endpoint_id,
            app_id=app_id,
            scope=scope,
            operation=operation,
            platform_tenant_id=platform_tenant_id,
            event_types=event_types,
            version=version,
            created_at=created_at,
            updated_at=updated_at,
        )

        operation_webhook_binding_response.additional_properties = d
        return operation_webhook_binding_response

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
