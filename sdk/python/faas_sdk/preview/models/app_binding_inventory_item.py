from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_binding_inventory_item_type import AppBindingInventoryItemType, check_app_binding_inventory_item_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_application_adoption import BindingApplicationAdoption
    from ..models.binding_refresh import BindingRefresh
    from ..models.binding_verification import BindingVerification
    from ..models.outbound_binding_probe_policy import OutboundBindingProbePolicy
    from ..models.queue_binding_canary import QueueBindingCanary
    from ..models.service_binding_inventory_context import ServiceBindingInventoryContext


T = TypeVar("T", bound="AppBindingInventoryItem")


@_attrs_define
class AppBindingInventoryItem:
    """Public binding configuration with separately reported runtime observations and verification status."""

    type_: AppBindingInventoryItemType
    name: str
    binding: str
    """Logical binding name or environment key. Empty for outbound bindings."""
    scope: str
    """Resource environment scope or app for an app-wide binding."""
    access: str
    state: str
    """Configuration or provisioning state; never evidence of connectivity."""
    runtime_status: str
    """Last-known queue consumer liveness (healthy, stale or degraded), otherwise unknown."""
    verification_status: str
    """Latest platform canary: passed, failed, unknown or stale. Managed edges without a known verification
    contract remain unsupported. Deployment or configuration changes invalidate old evidence. Inventory does not run
    probes."""
    service_context: ServiceBindingInventoryContext | Unset = UNSET
    """Frozen deployment service edge and current reviewed route metadata. Only the named managed verification
    contract can qualify this context; ordinary app receipts cannot."""
    service_target_deployment_id: UUID | Unset = UNSET
    """Exact service recipient selected explicitly or resolved from the managed reviewed release."""
    queue_canary: QueueBindingCanary | Unset = UNSET
    """Retained deployment-bound receipt for an explicitly authorized push queue completion probe."""
    queue_canary_supported: bool | Unset = UNSET
    """Whether the exact selected deployment and current consumer can admit a canary."""
    queue_canary_reason: str | Unset = UNSET
    """Sanitized reason canary publication is unavailable."""
    queue_environment: str | Unset = UNSET
    """Explicit queue environment; empty means shared legacy binding. Distinguishes app-wide scope from an
    environment named app. Consumer-readiness policies require this field; older inventories omit it."""
    verification: BindingVerification | Unset = UNSET
    """Sanitized durable evidence from the latest admitted service, PostgreSQL or object-storage task guest canary.
    Object-storage verification records bucket listing or explicitly requested permission-aware object operations
    and cleanup; inspect checks for coverage. Resident application adoption is not checked."""
    refresh: BindingRefresh | Unset = UNSET
    """Durable rolling restart handoff for a pending PostgreSQL or object-storage rotation. Completion does not
    imply fresh resident instances or successful verification. Not_queued means no retained outbox record was found;
    unknown means progress could not be read."""
    application_adoption: BindingApplicationAdoption | Unset = UNSET
    """Metadata-only application self-attestations for managed PostgreSQL or object-storage secrets. Counts refer
    to workload/secret pairs. Reads use a single state snapshot and include missing reports. Task guests, jobs,
    mirrors and unauthorized workloads are excluded. Receipts expire when secret versions change, independently of
    probe age. An older guest projection does not erase a newer application receipt. complete describes this
    optional observation read; a read failure leaves default checks unchanged and blocks strict checks."""
    observed_at: datetime.datetime | Unset = UNSET
    """Scheduler observation time. Absent when no observation exists."""
    http_url: str | Unset = UNSET
    https_env: str | Unset = UNSET
    https_url: str | Unset = UNSET
    transport: str | Unset = UNSET
    credential_generation: int | Unset = UNSET
    rotation_pending: bool | Unset = UNSET
    consumer_state: str | Unset = UNSET
    consumer_state_reason: str | Unset = UNSET
    consumer_liveness: str | Unset = UNSET
    outbound_probe: OutboundBindingProbePolicy | Unset = UNSET
    """Explicit provider endpoint declared safe to probe using managed outbound admission. Queries, redirects and
    unsuccessful expected statuses are unsupported."""
    credential_configured: bool | Unset = UNSET
    allowed_methods: list[str] | Unset = UNSET
    allowed_path_prefixes: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        name = self.name

        binding = self.binding

        scope = self.scope

        access = self.access

        state = self.state

        runtime_status = self.runtime_status

        verification_status = self.verification_status

        service_context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_context, Unset):
            service_context = self.service_context.to_dict()

        service_target_deployment_id: str | Unset = UNSET
        if not isinstance(self.service_target_deployment_id, Unset):
            service_target_deployment_id = str(self.service_target_deployment_id)

        queue_canary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.queue_canary, Unset):
            queue_canary = self.queue_canary.to_dict()

        queue_canary_supported = self.queue_canary_supported

        queue_canary_reason = self.queue_canary_reason

        queue_environment = self.queue_environment

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        refresh: dict[str, Any] | Unset = UNSET
        if not isinstance(self.refresh, Unset):
            refresh = self.refresh.to_dict()

        application_adoption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.application_adoption, Unset):
            application_adoption = self.application_adoption.to_dict()

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        http_url = self.http_url

        https_env = self.https_env

        https_url = self.https_url

        transport = self.transport

        credential_generation = self.credential_generation

        rotation_pending = self.rotation_pending

        consumer_state = self.consumer_state

        consumer_state_reason = self.consumer_state_reason

        consumer_liveness = self.consumer_liveness

        outbound_probe: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outbound_probe, Unset):
            outbound_probe = self.outbound_probe.to_dict()

        credential_configured = self.credential_configured

        allowed_methods: list[str] | Unset = UNSET
        if not isinstance(self.allowed_methods, Unset):
            allowed_methods = self.allowed_methods

        allowed_path_prefixes: list[str] | Unset = UNSET
        if not isinstance(self.allowed_path_prefixes, Unset):
            allowed_path_prefixes = self.allowed_path_prefixes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "binding": binding,
                "scope": scope,
                "access": access,
                "state": state,
                "runtime_status": runtime_status,
                "verification_status": verification_status,
            }
        )
        if service_context is not UNSET:
            field_dict["service_context"] = service_context
        if service_target_deployment_id is not UNSET:
            field_dict["service_target_deployment_id"] = service_target_deployment_id
        if queue_canary is not UNSET:
            field_dict["queue_canary"] = queue_canary
        if queue_canary_supported is not UNSET:
            field_dict["queue_canary_supported"] = queue_canary_supported
        if queue_canary_reason is not UNSET:
            field_dict["queue_canary_reason"] = queue_canary_reason
        if queue_environment is not UNSET:
            field_dict["queue_environment"] = queue_environment
        if verification is not UNSET:
            field_dict["verification"] = verification
        if refresh is not UNSET:
            field_dict["refresh"] = refresh
        if application_adoption is not UNSET:
            field_dict["application_adoption"] = application_adoption
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if http_url is not UNSET:
            field_dict["http_url"] = http_url
        if https_env is not UNSET:
            field_dict["https_env"] = https_env
        if https_url is not UNSET:
            field_dict["https_url"] = https_url
        if transport is not UNSET:
            field_dict["transport"] = transport
        if credential_generation is not UNSET:
            field_dict["credential_generation"] = credential_generation
        if rotation_pending is not UNSET:
            field_dict["rotation_pending"] = rotation_pending
        if consumer_state is not UNSET:
            field_dict["consumer_state"] = consumer_state
        if consumer_state_reason is not UNSET:
            field_dict["consumer_state_reason"] = consumer_state_reason
        if consumer_liveness is not UNSET:
            field_dict["consumer_liveness"] = consumer_liveness
        if outbound_probe is not UNSET:
            field_dict["outbound_probe"] = outbound_probe
        if credential_configured is not UNSET:
            field_dict["credential_configured"] = credential_configured
        if allowed_methods is not UNSET:
            field_dict["allowed_methods"] = allowed_methods
        if allowed_path_prefixes is not UNSET:
            field_dict["allowed_path_prefixes"] = allowed_path_prefixes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_application_adoption import BindingApplicationAdoption
        from ..models.binding_refresh import BindingRefresh
        from ..models.binding_verification import BindingVerification
        from ..models.outbound_binding_probe_policy import OutboundBindingProbePolicy
        from ..models.queue_binding_canary import QueueBindingCanary
        from ..models.service_binding_inventory_context import ServiceBindingInventoryContext

        d = dict(src_dict)
        type_ = check_app_binding_inventory_item_type(d.pop("type"))

        name = d.pop("name")

        binding = d.pop("binding")

        scope = d.pop("scope")

        access = d.pop("access")

        state = d.pop("state")

        runtime_status = d.pop("runtime_status")

        verification_status = d.pop("verification_status")

        _service_context = d.pop("service_context", UNSET)
        service_context: ServiceBindingInventoryContext | Unset
        if isinstance(_service_context, Unset):
            service_context = UNSET
        else:
            service_context = ServiceBindingInventoryContext.from_dict(_service_context)

        _service_target_deployment_id = d.pop("service_target_deployment_id", UNSET)
        service_target_deployment_id: UUID | Unset
        if isinstance(_service_target_deployment_id, Unset):
            service_target_deployment_id = UNSET
        else:
            service_target_deployment_id = UUID(_service_target_deployment_id)

        _queue_canary = d.pop("queue_canary", UNSET)
        queue_canary: QueueBindingCanary | Unset
        if isinstance(_queue_canary, Unset):
            queue_canary = UNSET
        else:
            queue_canary = QueueBindingCanary.from_dict(_queue_canary)

        queue_canary_supported = d.pop("queue_canary_supported", UNSET)

        queue_canary_reason = d.pop("queue_canary_reason", UNSET)

        queue_environment = d.pop("queue_environment", UNSET)

        _verification = d.pop("verification", UNSET)
        verification: BindingVerification | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = BindingVerification.from_dict(_verification)

        _refresh = d.pop("refresh", UNSET)
        refresh: BindingRefresh | Unset
        if isinstance(_refresh, Unset):
            refresh = UNSET
        else:
            refresh = BindingRefresh.from_dict(_refresh)

        _application_adoption = d.pop("application_adoption", UNSET)
        application_adoption: BindingApplicationAdoption | Unset
        if isinstance(_application_adoption, Unset):
            application_adoption = UNSET
        else:
            application_adoption = BindingApplicationAdoption.from_dict(_application_adoption)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        http_url = d.pop("http_url", UNSET)

        https_env = d.pop("https_env", UNSET)

        https_url = d.pop("https_url", UNSET)

        transport = d.pop("transport", UNSET)

        credential_generation = d.pop("credential_generation", UNSET)

        rotation_pending = d.pop("rotation_pending", UNSET)

        consumer_state = d.pop("consumer_state", UNSET)

        consumer_state_reason = d.pop("consumer_state_reason", UNSET)

        consumer_liveness = d.pop("consumer_liveness", UNSET)

        _outbound_probe = d.pop("outbound_probe", UNSET)
        outbound_probe: OutboundBindingProbePolicy | Unset
        if isinstance(_outbound_probe, Unset):
            outbound_probe = UNSET
        else:
            outbound_probe = OutboundBindingProbePolicy.from_dict(_outbound_probe)

        credential_configured = d.pop("credential_configured", UNSET)

        allowed_methods = cast(list[str], d.pop("allowed_methods", UNSET))

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes", UNSET))

        app_binding_inventory_item = cls(
            type_=type_,
            name=name,
            binding=binding,
            scope=scope,
            access=access,
            state=state,
            runtime_status=runtime_status,
            verification_status=verification_status,
            service_context=service_context,
            service_target_deployment_id=service_target_deployment_id,
            queue_canary=queue_canary,
            queue_canary_supported=queue_canary_supported,
            queue_canary_reason=queue_canary_reason,
            queue_environment=queue_environment,
            verification=verification,
            refresh=refresh,
            application_adoption=application_adoption,
            observed_at=observed_at,
            http_url=http_url,
            https_env=https_env,
            https_url=https_url,
            transport=transport,
            credential_generation=credential_generation,
            rotation_pending=rotation_pending,
            consumer_state=consumer_state,
            consumer_state_reason=consumer_state_reason,
            consumer_liveness=consumer_liveness,
            outbound_probe=outbound_probe,
            credential_configured=credential_configured,
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
        )

        app_binding_inventory_item.additional_properties = d
        return app_binding_inventory_item

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
