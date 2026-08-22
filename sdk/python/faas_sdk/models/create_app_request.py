from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_app_request_app_protocol import CreateAppRequestAppProtocol, check_create_app_request_app_protocol
from ..models.create_app_request_eviction_priority import (
    CreateAppRequestEvictionPriority,
    check_create_app_request_eviction_priority,
)
from ..models.create_app_request_runtime import CreateAppRequestRuntime, check_create_app_request_runtime
from ..models.create_app_request_type import CreateAppRequestType, check_create_app_request_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppRequest")


@_attrs_define
class CreateAppRequest:
    """App creation payload: slug, type (app|function), runtime (only for function), RAM MB, max concurrency, idle timeout,
    and optional manifest.

    """

    slug: str
    type_: CreateAppRequestType | Unset = UNSET
    runtime: CreateAppRequestRuntime | Unset = UNSET
    ram_mb: int | Unset = UNSET
    max_concurrency: int | Unset = UNSET
    idle_timeout_s: int | Unset = UNSET
    streaming_enabled: bool | Unset = UNSET
    """Per-app streaming flag. Omitted at create-time → apid applies the plan default (issue #471)."""
    websocket_enabled: bool | Unset = UNSET
    """Per-app raw-bytes Upgrade bridge flag (issue #676 / ADR-080). Omitted → apid applies the plan default;
    PATCH-true on Free is rejected by apid with 403 plan_websocket_not_allowed."""
    route_metrics_enabled: bool | Unset = UNSET
    """Per-app per-route observability flag (ADR-093). Omitted → apid applies the plan default (Free = false;
    Hobby/Pro/Scale = true). PATCH-true on Free is rejected by apid with 403 plan_route_metrics_not_allowed."""
    maintenance_mode: bool | Unset = UNSET
    """Coarse per-app maintenance toggle (ADR-091 amendment). Omitted → apid applies the default (false). Free-tier
    allowed; no plan gate. Flipping this on at create time pins the app for maintenance from the first request."""
    app_protocol: CreateAppRequestAppProtocol | Unset = UNSET
    """Per-app wire-protocol selector (ADR-124). Closed set {http1, http2, grpc}. Omit to use the per-plan default
    ('http1'); set explicitly to opt in to http2 or grpc. Free customers POSTing 'grpc' are rejected with 403
    plan_app_protocol_grpc_not_allowed."""
    warm_snapshot_enabled: bool | Unset = UNSET
    """Per-app two-tier snapshot flag (issue #470 / ADR-055). Omitted at create-time → apid applies the plan
    default. Free/Hobby PATCH-true is rejected."""
    warm_snapshot_min_requests: int | Unset = UNSET
    """Optional create-time override for the warm-tier request-count threshold (issue #470 / ADR-055). Range [1,
    100]. Omitted → apid applies the plan default."""
    warm_snapshot_min_ms: int | Unset = UNSET
    """Optional create-time override for the warm-tier time-since-first-ready threshold, milliseconds (issue #470 /
    ADR-055). Range [100, 60000]. Omitted → apid applies the plan default."""
    eviction_priority: CreateAppRequestEvictionPriority | Unset = UNSET
    """Per-app eviction tier (issue #475). 'best_effort' (default) keeps the pre-#475 LRU-by-last_request_at reaper
    behaviour; 'reserved' protects the app from cross-account RAM-pressure eviction. Omitted at create-time → apid
    applies the schema default 'best_effort'."""
    overflow_node: str | Unset = UNSET
    """Per-app preferred spill target for cross-node pressure rebalance (Tier A10 / ADR-088). Wire form is
    compute_nodes.name (resolved server-side). Omitted → no preference; empty string at create-time is rejected with
    422 invalid_overflow_node because the column starts NULL and there is no 'clear' path at create-time."""
    require_authn: bool | Unset = UNSET
    """Per-deployment token-gate flag (issue #560). Omitted at create-time → apid applies the plan default (false).
    Pro/Scale only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        type_: str | Unset = UNSET
        if not isinstance(self.type_, Unset):
            type_ = self.type_

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        ram_mb = self.ram_mb

        max_concurrency = self.max_concurrency

        idle_timeout_s = self.idle_timeout_s

        streaming_enabled = self.streaming_enabled

        websocket_enabled = self.websocket_enabled

        route_metrics_enabled = self.route_metrics_enabled

        maintenance_mode = self.maintenance_mode

        app_protocol: str | Unset = UNSET
        if not isinstance(self.app_protocol, Unset):
            app_protocol = self.app_protocol

        warm_snapshot_enabled = self.warm_snapshot_enabled

        warm_snapshot_min_requests = self.warm_snapshot_min_requests

        warm_snapshot_min_ms = self.warm_snapshot_min_ms

        eviction_priority: str | Unset = UNSET
        if not isinstance(self.eviction_priority, Unset):
            eviction_priority = self.eviction_priority

        overflow_node = self.overflow_node

        require_authn = self.require_authn

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
            }
        )
        if type_ is not UNSET:
            field_dict["type"] = type_
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if idle_timeout_s is not UNSET:
            field_dict["idle_timeout_s"] = idle_timeout_s
        if streaming_enabled is not UNSET:
            field_dict["streaming_enabled"] = streaming_enabled
        if websocket_enabled is not UNSET:
            field_dict["websocket_enabled"] = websocket_enabled
        if route_metrics_enabled is not UNSET:
            field_dict["route_metrics_enabled"] = route_metrics_enabled
        if maintenance_mode is not UNSET:
            field_dict["maintenance_mode"] = maintenance_mode
        if app_protocol is not UNSET:
            field_dict["app_protocol"] = app_protocol
        if warm_snapshot_enabled is not UNSET:
            field_dict["warm_snapshot_enabled"] = warm_snapshot_enabled
        if warm_snapshot_min_requests is not UNSET:
            field_dict["warm_snapshot_min_requests"] = warm_snapshot_min_requests
        if warm_snapshot_min_ms is not UNSET:
            field_dict["warm_snapshot_min_ms"] = warm_snapshot_min_ms
        if eviction_priority is not UNSET:
            field_dict["eviction_priority"] = eviction_priority
        if overflow_node is not UNSET:
            field_dict["overflow_node"] = overflow_node
        if require_authn is not UNSET:
            field_dict["require_authn"] = require_authn

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        _type_ = d.pop("type", UNSET)
        type_: CreateAppRequestType | Unset
        if isinstance(_type_, Unset):
            type_ = UNSET
        else:
            type_ = check_create_app_request_type(_type_)

        _runtime = d.pop("runtime", UNSET)
        runtime: CreateAppRequestRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_create_app_request_runtime(_runtime)

        ram_mb = d.pop("ram_mb", UNSET)

        max_concurrency = d.pop("max_concurrency", UNSET)

        idle_timeout_s = d.pop("idle_timeout_s", UNSET)

        streaming_enabled = d.pop("streaming_enabled", UNSET)

        websocket_enabled = d.pop("websocket_enabled", UNSET)

        route_metrics_enabled = d.pop("route_metrics_enabled", UNSET)

        maintenance_mode = d.pop("maintenance_mode", UNSET)

        _app_protocol = d.pop("app_protocol", UNSET)
        app_protocol: CreateAppRequestAppProtocol | Unset
        if isinstance(_app_protocol, Unset):
            app_protocol = UNSET
        else:
            app_protocol = check_create_app_request_app_protocol(_app_protocol)

        warm_snapshot_enabled = d.pop("warm_snapshot_enabled", UNSET)

        warm_snapshot_min_requests = d.pop("warm_snapshot_min_requests", UNSET)

        warm_snapshot_min_ms = d.pop("warm_snapshot_min_ms", UNSET)

        _eviction_priority = d.pop("eviction_priority", UNSET)
        eviction_priority: CreateAppRequestEvictionPriority | Unset
        if isinstance(_eviction_priority, Unset):
            eviction_priority = UNSET
        else:
            eviction_priority = check_create_app_request_eviction_priority(_eviction_priority)

        overflow_node = d.pop("overflow_node", UNSET)

        require_authn = d.pop("require_authn", UNSET)

        create_app_request = cls(
            slug=slug,
            type_=type_,
            runtime=runtime,
            ram_mb=ram_mb,
            max_concurrency=max_concurrency,
            idle_timeout_s=idle_timeout_s,
            streaming_enabled=streaming_enabled,
            websocket_enabled=websocket_enabled,
            route_metrics_enabled=route_metrics_enabled,
            maintenance_mode=maintenance_mode,
            app_protocol=app_protocol,
            warm_snapshot_enabled=warm_snapshot_enabled,
            warm_snapshot_min_requests=warm_snapshot_min_requests,
            warm_snapshot_min_ms=warm_snapshot_min_ms,
            eviction_priority=eviction_priority,
            overflow_node=overflow_node,
            require_authn=require_authn,
        )

        create_app_request.additional_properties = d
        return create_app_request

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
