from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_binding_transport import ServiceBindingTransport, check_service_binding_transport
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedServiceProbeSpec")


@_attrs_define
class ManagedServiceProbeSpec:
    """Server-selected frozen context for a managed service route probe. Contains no credentials or application request.
    Changing context or policy invalidates the receipt.

    """

    version: int
    """Managed probe contract version; currently 1."""
    caller_deployment_id: UUID
    scope: str
    service: str
    """Logical frozen service workload."""
    binding: str
    """Frozen environment key, including custom keys."""
    target_app_id: UUID
    target_deployment_id: UUID
    release_id: UUID
    context_revision: str
    transport: ServiceBindingTransport | Unset = UNSET
    """Scheme used by the canonical GREGALE_SERVICE_<NAME>_URL environment variable. `https` selects the private
    `.internal` alias; `http` preserves the legacy `.svc.gregale` endpoint."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        caller_deployment_id = str(self.caller_deployment_id)

        scope = self.scope

        service = self.service

        binding = self.binding

        target_app_id = str(self.target_app_id)

        target_deployment_id = str(self.target_deployment_id)

        release_id = str(self.release_id)

        context_revision = self.context_revision

        transport: str | Unset = UNSET
        if not isinstance(self.transport, Unset):
            transport = self.transport

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "caller_deployment_id": caller_deployment_id,
                "scope": scope,
                "service": service,
                "binding": binding,
                "target_app_id": target_app_id,
                "target_deployment_id": target_deployment_id,
                "release_id": release_id,
                "context_revision": context_revision,
            }
        )
        if transport is not UNSET:
            field_dict["transport"] = transport

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        caller_deployment_id = UUID(d.pop("caller_deployment_id"))

        scope = d.pop("scope")

        service = d.pop("service")

        binding = d.pop("binding")

        target_app_id = UUID(d.pop("target_app_id"))

        target_deployment_id = UUID(d.pop("target_deployment_id"))

        release_id = UUID(d.pop("release_id"))

        context_revision = d.pop("context_revision")

        _transport = d.pop("transport", UNSET)
        transport: ServiceBindingTransport | Unset
        if isinstance(_transport, Unset):
            transport = UNSET
        else:
            transport = check_service_binding_transport(_transport)

        managed_service_probe_spec = cls(
            version=version,
            caller_deployment_id=caller_deployment_id,
            scope=scope,
            service=service,
            binding=binding,
            target_app_id=target_app_id,
            target_deployment_id=target_deployment_id,
            release_id=release_id,
            context_revision=context_revision,
            transport=transport,
        )

        managed_service_probe_spec.additional_properties = d
        return managed_service_probe_spec

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
