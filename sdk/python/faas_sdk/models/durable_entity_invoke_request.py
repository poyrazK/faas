from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityInvokeRequest")


@_attrs_define
class DurableEntityInvokeRequest:
    """Preview entity invocation; request_id and payload identify durable retries."""

    namespace: str
    key: str
    request_id: str
    payload: Any
    """JSON payload; replay fingerprints the exact decoded JSON bytes."""
    environment: str | Unset = UNSET
    """Registered project environment; defaults to production. Standalone apps use their default scope."""
    platform_tenant_id: UUID | Unset = UNSET
    """Optional active customer owned by the authenticated account."""

    def to_dict(self) -> dict[str, Any]:
        namespace = self.namespace

        key = self.key

        request_id = self.request_id

        payload = self.payload

        environment = self.environment

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "namespace": namespace,
                "key": key,
                "request_id": request_id,
                "payload": payload,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        namespace = d.pop("namespace")

        key = d.pop("key")

        request_id = d.pop("request_id")

        payload = d.pop("payload")

        environment = d.pop("environment", UNSET)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        durable_entity_invoke_request = cls(
            namespace=namespace,
            key=key,
            request_id=request_id,
            payload=payload,
            environment=environment,
            platform_tenant_id=platform_tenant_id,
        )

        return durable_entity_invoke_request
