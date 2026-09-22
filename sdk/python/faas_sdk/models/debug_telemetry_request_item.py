from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_telemetry_request_item_method import (
    DebugTelemetryRequestItemMethod,
    check_debug_telemetry_request_item_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_guest_execution_evidence import DebugGuestExecutionEvidence


T = TypeVar("T", bound="DebugTelemetryRequestItem")


@_attrs_define
class DebugTelemetryRequestItem:
    """One bounded latency-bucket row representing gateway-served requests, persisted by the recorder/publisher."""

    id: UUID
    """Internal telemetry row UUID. Use trace_id as the customer-visible request identifier when present."""
    deployment_id: UUID
    route: str
    """Route template (NOT expanded URL)."""
    method: DebugTelemetryRequestItemMethod
    status: int
    latency_ms: int
    """Inclusive upper bound of the bounded latency bucket represented by this row."""
    count: int
    """Number of original requests represented by this collapsed telemetry row."""
    cold_boot: bool
    received_at: datetime.datetime
    trace_id: None | str | Unset = UNSET
    """Customer-visible x-faas-request-id and W3C trace-id (32 hex chars), null for legacy rows."""
    wake_id: str | Unset = UNSET
    """Opaque wake identifier when this request admitted a wake; omitted for warm requests."""
    instance_id: str | Unset = UNSET
    """Opaque instance identifier that served the request; omitted when no target was reached."""
    consumer_id: UUID | Unset = UNSET
    """Stable API consumer identity; omitted for anonymous or legacy traffic."""
    node_id: str | Unset = UNSET
    """Compute node that served the request."""
    region: str | Unset = UNSET
    """Region associated with the deployment target."""
    commit_sha: str | Unset = UNSET
    """Source revision associated with the deployment."""
    deployment_tag: str | Unset = UNSET
    """Deployment tag associated with the request."""
    deployment_created_at: str | Unset = UNSET
    """Deployment creation timestamp supplied by the platform."""
    image_digest: str | Unset = UNSET
    """Immutable image/artifact digest associated with the deployment."""
    guest: DebugGuestExecutionEvidence | Unset = UNSET
    """Bounded, platform-owned runtime execution evidence. Omitted when the runner signal was unavailable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        deployment_id = str(self.deployment_id)

        route = self.route

        method: str = self.method

        status = self.status

        latency_ms = self.latency_ms

        count = self.count

        cold_boot = self.cold_boot

        received_at = self.received_at.isoformat()

        trace_id: None | str | Unset
        if isinstance(self.trace_id, Unset):
            trace_id = UNSET
        else:
            trace_id = self.trace_id

        wake_id = self.wake_id

        instance_id = self.instance_id

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        node_id = self.node_id

        region = self.region

        commit_sha = self.commit_sha

        deployment_tag = self.deployment_tag

        deployment_created_at = self.deployment_created_at

        image_digest = self.image_digest

        guest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.guest, Unset):
            guest = self.guest.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "deployment_id": deployment_id,
                "route": route,
                "method": method,
                "status": status,
                "latency_ms": latency_ms,
                "count": count,
                "cold_boot": cold_boot,
                "received_at": received_at,
            }
        )
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if wake_id is not UNSET:
            field_dict["wake_id"] = wake_id
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if node_id is not UNSET:
            field_dict["node_id"] = node_id
        if region is not UNSET:
            field_dict["region"] = region
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if deployment_tag is not UNSET:
            field_dict["deployment_tag"] = deployment_tag
        if deployment_created_at is not UNSET:
            field_dict["deployment_created_at"] = deployment_created_at
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if guest is not UNSET:
            field_dict["guest"] = guest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_guest_execution_evidence import DebugGuestExecutionEvidence

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        deployment_id = UUID(d.pop("deployment_id"))

        route = d.pop("route")

        method = check_debug_telemetry_request_item_method(d.pop("method"))

        status = d.pop("status")

        latency_ms = d.pop("latency_ms")

        count = d.pop("count")

        cold_boot = d.pop("cold_boot")

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        def _parse_trace_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        trace_id = _parse_trace_id(d.pop("trace_id", UNSET))

        wake_id = d.pop("wake_id", UNSET)

        instance_id = d.pop("instance_id", UNSET)

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        node_id = d.pop("node_id", UNSET)

        region = d.pop("region", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        deployment_tag = d.pop("deployment_tag", UNSET)

        deployment_created_at = d.pop("deployment_created_at", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        _guest = d.pop("guest", UNSET)
        guest: DebugGuestExecutionEvidence | Unset
        if isinstance(_guest, Unset):
            guest = UNSET
        else:
            guest = DebugGuestExecutionEvidence.from_dict(_guest)

        debug_telemetry_request_item = cls(
            id=id,
            deployment_id=deployment_id,
            route=route,
            method=method,
            status=status,
            latency_ms=latency_ms,
            count=count,
            cold_boot=cold_boot,
            received_at=received_at,
            trace_id=trace_id,
            wake_id=wake_id,
            instance_id=instance_id,
            consumer_id=consumer_id,
            node_id=node_id,
            region=region,
            commit_sha=commit_sha,
            deployment_tag=deployment_tag,
            deployment_created_at=deployment_created_at,
            image_digest=image_digest,
            guest=guest,
        )

        debug_telemetry_request_item.additional_properties = d
        return debug_telemetry_request_item

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
