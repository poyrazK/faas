from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_binding_inventory_context_routing_status import (
    ServiceBindingInventoryContextRoutingStatus,
    check_service_binding_inventory_context_routing_status,
)
from ..models.service_binding_inventory_context_source import (
    ServiceBindingInventoryContextSource,
    check_service_binding_inventory_context_source,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ServiceBindingInventoryContext")


@_attrs_define
class ServiceBindingInventoryContext:
    """Frozen deployment service edge and current reviewed route metadata. Only the named managed verification contract can
    qualify this context; ordinary app receipts cannot.

    """

    source: ServiceBindingInventoryContextSource
    caller_deployment_id: UUID
    revision: str
    """Opaque digest of reviewed graph identities and public edge facts; changes invalidate a verification review.
    Not a connectivity receipt or authorization capability."""
    routing_status: ServiceBindingInventoryContextRoutingStatus
    routing_reason: str
    """Stable sanitized reason for managed route resolution."""
    verification_supported: bool
    """True only for an active request/service or worker caller with a supported frozen HTTPS edge to a
    request/service target."""
    verification_reason: str
    """Empty for a supported managed contract; otherwise managed_service_verification_unsupported."""
    target_app_id: UUID | Unset = UNSET
    """Account-owned frozen target app; omitted when unreadable or foreign."""
    target_app: str | Unset = UNSET
    """Physical target app slug; the binding name remains the logical workload."""
    release_id: UUID | Unset = UNSET
    """Active reviewed release selected by the managed route resolver, when available."""
    verification_contract: str | Unset = UNSET
    """managed-https-v1 identifies the supported managed guest probe contract. Missing or unknown contracts remain
    unsupported."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        caller_deployment_id = str(self.caller_deployment_id)

        revision = self.revision

        routing_status: str = self.routing_status

        routing_reason = self.routing_reason

        verification_supported = self.verification_supported

        verification_reason = self.verification_reason

        target_app_id: str | Unset = UNSET
        if not isinstance(self.target_app_id, Unset):
            target_app_id = str(self.target_app_id)

        target_app = self.target_app

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

        verification_contract = self.verification_contract

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "caller_deployment_id": caller_deployment_id,
                "revision": revision,
                "routing_status": routing_status,
                "routing_reason": routing_reason,
                "verification_supported": verification_supported,
                "verification_reason": verification_reason,
            }
        )
        if target_app_id is not UNSET:
            field_dict["target_app_id"] = target_app_id
        if target_app is not UNSET:
            field_dict["target_app"] = target_app
        if release_id is not UNSET:
            field_dict["release_id"] = release_id
        if verification_contract is not UNSET:
            field_dict["verification_contract"] = verification_contract

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_service_binding_inventory_context_source(d.pop("source"))

        caller_deployment_id = UUID(d.pop("caller_deployment_id"))

        revision = d.pop("revision")

        routing_status = check_service_binding_inventory_context_routing_status(d.pop("routing_status"))

        routing_reason = d.pop("routing_reason")

        verification_supported = d.pop("verification_supported")

        verification_reason = d.pop("verification_reason")

        _target_app_id = d.pop("target_app_id", UNSET)
        target_app_id: UUID | Unset
        if isinstance(_target_app_id, Unset):
            target_app_id = UNSET
        else:
            target_app_id = UUID(_target_app_id)

        target_app = d.pop("target_app", UNSET)

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

        verification_contract = d.pop("verification_contract", UNSET)

        service_binding_inventory_context = cls(
            source=source,
            caller_deployment_id=caller_deployment_id,
            revision=revision,
            routing_status=routing_status,
            routing_reason=routing_reason,
            verification_supported=verification_supported,
            verification_reason=verification_reason,
            target_app_id=target_app_id,
            target_app=target_app,
            release_id=release_id,
            verification_contract=verification_contract,
        )

        service_binding_inventory_context.additional_properties = d
        return service_binding_inventory_context

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
