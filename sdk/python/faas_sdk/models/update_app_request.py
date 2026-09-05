from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_app_request_app_protocol import UpdateAppRequestAppProtocol, check_update_app_request_app_protocol
from ..models.update_app_request_cpu_millicores_type_1 import (
    UpdateAppRequestCpuMillicoresType1,
    check_update_app_request_cpu_millicores_type_1,
)
from ..models.update_app_request_cpu_millicores_type_2_type_1 import (
    UpdateAppRequestCpuMillicoresType2Type1,
    check_update_app_request_cpu_millicores_type_2_type_1,
)
from ..models.update_app_request_cpu_millicores_type_3_type_1 import (
    UpdateAppRequestCpuMillicoresType3Type1,
    check_update_app_request_cpu_millicores_type_3_type_1,
)
from ..models.update_app_request_eviction_priority_type_1 import (
    UpdateAppRequestEvictionPriorityType1,
    check_update_app_request_eviction_priority_type_1,
)
from ..models.update_app_request_eviction_priority_type_2_type_1 import (
    UpdateAppRequestEvictionPriorityType2Type1,
    check_update_app_request_eviction_priority_type_2_type_1,
)
from ..models.update_app_request_eviction_priority_type_3_type_1 import (
    UpdateAppRequestEvictionPriorityType3Type1,
    check_update_app_request_eviction_priority_type_3_type_1,
)
from ..models.update_app_request_execution_mode_type_1 import (
    UpdateAppRequestExecutionModeType1,
    check_update_app_request_execution_mode_type_1,
)
from ..models.update_app_request_execution_mode_type_2_type_1 import (
    UpdateAppRequestExecutionModeType2Type1,
    check_update_app_request_execution_mode_type_2_type_1,
)
from ..models.update_app_request_execution_mode_type_3_type_1 import (
    UpdateAppRequestExecutionModeType3Type1,
    check_update_app_request_execution_mode_type_3_type_1,
)
from ..models.update_app_request_restart_policy_type_1 import (
    UpdateAppRequestRestartPolicyType1,
    check_update_app_request_restart_policy_type_1,
)
from ..models.update_app_request_restart_policy_type_2_type_1 import (
    UpdateAppRequestRestartPolicyType2Type1,
    check_update_app_request_restart_policy_type_2_type_1,
)
from ..models.update_app_request_restart_policy_type_3_type_1 import (
    UpdateAppRequestRestartPolicyType3Type1,
    check_update_app_request_restart_policy_type_3_type_1,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.public_auth_block import PublicAuthBlock
    from ..models.scaling_policy import ScalingPolicy
    from ..models.service_replicas import ServiceReplicas


T = TypeVar("T", bound="UpdateAppRequest")


@_attrs_define
class UpdateAppRequest:
    """Partial update — every field is optional; omitted fields are unchanged."""

    ram_mb: int | None | Unset = UNSET
    cpu_millicores: (
        None
        | Unset
        | UpdateAppRequestCpuMillicoresType1
        | UpdateAppRequestCpuMillicoresType2Type1
        | UpdateAppRequestCpuMillicoresType3Type1
    ) = UNSET
    """Sustained CPU allowance per instance. Omit for no change."""
    idle_timeout_s: int | None | Unset = UNSET
    max_concurrency: int | None | Unset = UNSET
    execution_mode: (
        None
        | Unset
        | UpdateAppRequestExecutionModeType1
        | UpdateAppRequestExecutionModeType2Type1
        | UpdateAppRequestExecutionModeType3Type1
    ) = UNSET
    """Lifecycle contract for the app. Omit for no change; service/worker/job are plan-gated."""
    restart_policy: (
        None
        | Unset
        | UpdateAppRequestRestartPolicyType1
        | UpdateAppRequestRestartPolicyType2Type1
        | UpdateAppRequestRestartPolicyType3Type1
    ) = UNSET
    """Restart behavior for the workload. Omit for no change."""
    startup_deadline_s: int | None | Unset = UNSET
    """Upper bound on time-to-ready in seconds. Omit for no change; 0 uses the plan default."""
    max_retries: int | None | Unset = UNSET
    """Maximum consecutive restart attempts. Omit for no change; 0 uses the plan default."""
    service_replicas: ServiceReplicas | Unset = UNSET
    """Per-deployment replica scaffold for execution_mode='service' (ADR-137 §Decision 3, M-2 + M-4 workstream E).
    Replica count is bounded by ServiceReplicasMax per plan (Hobby 3, Pro 5, Scale 20), and desired must also fit
    the app's max_concurrency ceiling. min ≤ desired ≤ max must hold. Foundation here; rolling-deploy / rollback /
    image-digest pinning semantics land in M-4."""
    min_instances: int | None | Unset = UNSET
    egress_allowlist: list[str] | Unset = UNSET
    """v4 or v6 CIDR allowlist; empty array clears to chain-default-accept."""
    autoscale_target_rps: int | None | Unset = UNSET
    """Per-instance RPS target for the reactive scale-up trigger. 0 = disable. Hobby/Pro/Scale only. Values < 0 are
    422 invalid_autoscale_target_rps."""
    autoscale_target_cpu_pct: int | None | Unset = UNSET
    """Per-instance CPU% target (1..100, 0 = disable) for the reactive scale-up trigger. Pro/Scale only. Values
    outside [1, 100] (other than 0) are 422 invalid_autoscale_target_cpu_pct."""
    streaming_enabled: bool | None | Unset = UNSET
    """Per-app streaming flag (issue #471). Omitted → no change. Free PATCHing true is 403
    plan_streaming_not_allowed."""
    websocket_enabled: bool | None | Unset = UNSET
    """Per-app raw-bytes Upgrade bridge flag (issue #676 / ADR-080). Omitted → no change. Free PATCHing true is 403
    plan_websocket_not_allowed."""
    route_metrics_enabled: bool | None | Unset = UNSET
    """Per-app per-route observability flag (ADR-093). Omitted → no change. Free PATCHing true is 403
    plan_route_metrics_not_allowed."""
    maintenance_mode: bool | None | Unset = UNSET
    """Coarse per-app maintenance toggle (ADR-091 amendment). Omitted → no change. PATCH true pins the app for
    maintenance (every request 503 + Retry-After); PATCH false restores normal handling. Free-tier allowed; no plan
    gate. The apps_maintenance_mode_notify trigger (migration 00237) fires pg_notify on flip."""
    app_protocol: UpdateAppRequestAppProtocol | Unset = UNSET
    """Per-app wire-protocol selector (ADR-124). Closed set {http1, http2, grpc}. Omit for no change; set
    explicitly to opt in (http2/grpc) or reset to 'http1'. Free customers PATCHing 'grpc' are rejected with 403
    plan_app_protocol_grpc_not_allowed."""
    scaling_policy: None | ScalingPolicy | Unset = UNSET
    """Per-app scaling policy. Omitted → no change. Non-null → atomic full-overwrite of the jsonb column."""
    require_signed: bool | None | Unset = UNSET
    """DEPRECATED on this surface. The customer PATCH /v1/apps/{slug} endpoint silently drops require_signed; the
    operator endpoint PATCH /v1/apps/{slug}/security is the only path that flips the flag (issue #472 / ADR-054).
    The field is parsed for backwards compatibility but never persisted from this endpoint."""
    warm_snapshot_enabled: bool | None | Unset = UNSET
    """Per-app two-tier snapshot flag (issue #470 / ADR-055). Omitted → no change. PATCH-true on Free/Hobby is
    rejected with 403 plan_warm_snapshot_not_allowed."""
    warm_snapshot_min_requests: int | None | Unset = UNSET
    """Per-app request-count threshold for warm-tier capture (issue #470 / ADR-055). Range [1, 100]. Omitted → no
    change."""
    warm_snapshot_min_ms: int | None | Unset = UNSET
    """Per-app time-since-first-ready threshold for warm-tier capture, milliseconds (issue #470 / ADR-055). Range
    [100, 60000]. Omitted → no change."""
    eviction_priority: (
        None
        | Unset
        | UpdateAppRequestEvictionPriorityType1
        | UpdateAppRequestEvictionPriorityType2Type1
        | UpdateAppRequestEvictionPriorityType3Type1
    ) = UNSET
    """Per-app eviction tier (issue #475). 'best_effort' (default) keeps the pre-#475 LRU-by-last_request_at reaper
    behaviour; 'reserved' protects the app from cross-account RAM-pressure eviction (every best_effort candidate is
    drained before any reserved is parked). Plan-gated upstream: Free PATCH 'reserved' returns 402
    plan_eviction_priority_reserved_not_allowed. Per-account cap (Hobby 1, Pro 2, Scale 4): 422
    plan_eviction_priority_reserved_quota when exhausted. Omitted → no change."""
    require_authn: bool | None | Unset = UNSET
    """Per-deployment token-gate flag (issue #560). Omitted → no change. PATCH-true on Free/Hobby is rejected with
    403 plan_require_authn_not_allowed."""
    public_auth: None | PublicAuthBlock | Unset = UNSET
    """Per-app public-URL auth configuration (issue #477 / ADR-077). Omitted → no change. When present, mode is the
    closed enum {open, bearer, basic}; basic_user + basic_pass are required when mode='basic' and the apid seal step
    encrypts them under the APP_BASIC_AUTH secretbox namespace before persistence."""
    overflow_node: None | str | Unset = UNSET
    """Per-app preferred spill target for cross-node pressure rebalance (Tier A10 / ADR-088). Wire form is the
    human-readable compute_nodes.name; apid resolves to UUID server-side. Tri-state: omitted → no change; empty
    string → clear (back to A9 fallback); non-empty → resolve name → UUID via Store.ComputeNodeByName and persist
    the UUID. 404 on unknown name; 422 on inactive node."""
    cors_default_enabled: bool | None | Unset = UNSET
    cors_default_origins: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.public_auth_block import PublicAuthBlock
        from ..models.scaling_policy import ScalingPolicy

        ram_mb: int | None | Unset
        if isinstance(self.ram_mb, Unset):
            ram_mb = UNSET
        else:
            ram_mb = self.ram_mb

        cpu_millicores: int | None | Unset
        if isinstance(self.cpu_millicores, Unset):
            cpu_millicores = UNSET
        elif isinstance(self.cpu_millicores, int):
            cpu_millicores = self.cpu_millicores
        elif isinstance(self.cpu_millicores, int):
            cpu_millicores = self.cpu_millicores
        elif isinstance(self.cpu_millicores, int):
            cpu_millicores = self.cpu_millicores
        else:
            cpu_millicores = self.cpu_millicores

        idle_timeout_s: int | None | Unset
        if isinstance(self.idle_timeout_s, Unset):
            idle_timeout_s = UNSET
        else:
            idle_timeout_s = self.idle_timeout_s

        max_concurrency: int | None | Unset
        if isinstance(self.max_concurrency, Unset):
            max_concurrency = UNSET
        else:
            max_concurrency = self.max_concurrency

        execution_mode: None | str | Unset
        if isinstance(self.execution_mode, Unset):
            execution_mode = UNSET
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        else:
            execution_mode = self.execution_mode

        restart_policy: None | str | Unset
        if isinstance(self.restart_policy, Unset):
            restart_policy = UNSET
        elif isinstance(self.restart_policy, str):
            restart_policy = self.restart_policy
        elif isinstance(self.restart_policy, str):
            restart_policy = self.restart_policy
        elif isinstance(self.restart_policy, str):
            restart_policy = self.restart_policy
        else:
            restart_policy = self.restart_policy

        startup_deadline_s: int | None | Unset
        if isinstance(self.startup_deadline_s, Unset):
            startup_deadline_s = UNSET
        else:
            startup_deadline_s = self.startup_deadline_s

        max_retries: int | None | Unset
        if isinstance(self.max_retries, Unset):
            max_retries = UNSET
        else:
            max_retries = self.max_retries

        service_replicas: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_replicas, Unset):
            service_replicas = self.service_replicas.to_dict()

        min_instances: int | None | Unset
        if isinstance(self.min_instances, Unset):
            min_instances = UNSET
        else:
            min_instances = self.min_instances

        egress_allowlist: list[str] | Unset = UNSET
        if not isinstance(self.egress_allowlist, Unset):
            egress_allowlist = self.egress_allowlist

        autoscale_target_rps: int | None | Unset
        if isinstance(self.autoscale_target_rps, Unset):
            autoscale_target_rps = UNSET
        else:
            autoscale_target_rps = self.autoscale_target_rps

        autoscale_target_cpu_pct: int | None | Unset
        if isinstance(self.autoscale_target_cpu_pct, Unset):
            autoscale_target_cpu_pct = UNSET
        else:
            autoscale_target_cpu_pct = self.autoscale_target_cpu_pct

        streaming_enabled: bool | None | Unset
        if isinstance(self.streaming_enabled, Unset):
            streaming_enabled = UNSET
        else:
            streaming_enabled = self.streaming_enabled

        websocket_enabled: bool | None | Unset
        if isinstance(self.websocket_enabled, Unset):
            websocket_enabled = UNSET
        else:
            websocket_enabled = self.websocket_enabled

        route_metrics_enabled: bool | None | Unset
        if isinstance(self.route_metrics_enabled, Unset):
            route_metrics_enabled = UNSET
        else:
            route_metrics_enabled = self.route_metrics_enabled

        maintenance_mode: bool | None | Unset
        if isinstance(self.maintenance_mode, Unset):
            maintenance_mode = UNSET
        else:
            maintenance_mode = self.maintenance_mode

        app_protocol: str | Unset = UNSET
        if not isinstance(self.app_protocol, Unset):
            app_protocol = self.app_protocol

        scaling_policy: dict[str, Any] | None | Unset
        if isinstance(self.scaling_policy, Unset):
            scaling_policy = UNSET
        elif isinstance(self.scaling_policy, ScalingPolicy):
            scaling_policy = self.scaling_policy.to_dict()
        else:
            scaling_policy = self.scaling_policy

        require_signed: bool | None | Unset
        if isinstance(self.require_signed, Unset):
            require_signed = UNSET
        else:
            require_signed = self.require_signed

        warm_snapshot_enabled: bool | None | Unset
        if isinstance(self.warm_snapshot_enabled, Unset):
            warm_snapshot_enabled = UNSET
        else:
            warm_snapshot_enabled = self.warm_snapshot_enabled

        warm_snapshot_min_requests: int | None | Unset
        if isinstance(self.warm_snapshot_min_requests, Unset):
            warm_snapshot_min_requests = UNSET
        else:
            warm_snapshot_min_requests = self.warm_snapshot_min_requests

        warm_snapshot_min_ms: int | None | Unset
        if isinstance(self.warm_snapshot_min_ms, Unset):
            warm_snapshot_min_ms = UNSET
        else:
            warm_snapshot_min_ms = self.warm_snapshot_min_ms

        eviction_priority: None | str | Unset
        if isinstance(self.eviction_priority, Unset):
            eviction_priority = UNSET
        elif isinstance(self.eviction_priority, str):
            eviction_priority = self.eviction_priority
        elif isinstance(self.eviction_priority, str):
            eviction_priority = self.eviction_priority
        elif isinstance(self.eviction_priority, str):
            eviction_priority = self.eviction_priority
        else:
            eviction_priority = self.eviction_priority

        require_authn: bool | None | Unset
        if isinstance(self.require_authn, Unset):
            require_authn = UNSET
        else:
            require_authn = self.require_authn

        public_auth: dict[str, Any] | None | Unset
        if isinstance(self.public_auth, Unset):
            public_auth = UNSET
        elif isinstance(self.public_auth, PublicAuthBlock):
            public_auth = self.public_auth.to_dict()
        else:
            public_auth = self.public_auth

        overflow_node: None | str | Unset
        if isinstance(self.overflow_node, Unset):
            overflow_node = UNSET
        else:
            overflow_node = self.overflow_node

        cors_default_enabled: bool | None | Unset
        if isinstance(self.cors_default_enabled, Unset):
            cors_default_enabled = UNSET
        else:
            cors_default_enabled = self.cors_default_enabled

        cors_default_origins: list[str] | Unset = UNSET
        if not isinstance(self.cors_default_origins, Unset):
            cors_default_origins = self.cors_default_origins

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if cpu_millicores is not UNSET:
            field_dict["cpu_millicores"] = cpu_millicores
        if idle_timeout_s is not UNSET:
            field_dict["idle_timeout_s"] = idle_timeout_s
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if execution_mode is not UNSET:
            field_dict["execution_mode"] = execution_mode
        if restart_policy is not UNSET:
            field_dict["restart_policy"] = restart_policy
        if startup_deadline_s is not UNSET:
            field_dict["startup_deadline_s"] = startup_deadline_s
        if max_retries is not UNSET:
            field_dict["max_retries"] = max_retries
        if service_replicas is not UNSET:
            field_dict["service_replicas"] = service_replicas
        if min_instances is not UNSET:
            field_dict["min_instances"] = min_instances
        if egress_allowlist is not UNSET:
            field_dict["egress_allowlist"] = egress_allowlist
        if autoscale_target_rps is not UNSET:
            field_dict["autoscale_target_rps"] = autoscale_target_rps
        if autoscale_target_cpu_pct is not UNSET:
            field_dict["autoscale_target_cpu_pct"] = autoscale_target_cpu_pct
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
        if scaling_policy is not UNSET:
            field_dict["scaling_policy"] = scaling_policy
        if require_signed is not UNSET:
            field_dict["require_signed"] = require_signed
        if warm_snapshot_enabled is not UNSET:
            field_dict["warm_snapshot_enabled"] = warm_snapshot_enabled
        if warm_snapshot_min_requests is not UNSET:
            field_dict["warm_snapshot_min_requests"] = warm_snapshot_min_requests
        if warm_snapshot_min_ms is not UNSET:
            field_dict["warm_snapshot_min_ms"] = warm_snapshot_min_ms
        if eviction_priority is not UNSET:
            field_dict["eviction_priority"] = eviction_priority
        if require_authn is not UNSET:
            field_dict["require_authn"] = require_authn
        if public_auth is not UNSET:
            field_dict["public_auth"] = public_auth
        if overflow_node is not UNSET:
            field_dict["overflow_node"] = overflow_node
        if cors_default_enabled is not UNSET:
            field_dict["cors_default_enabled"] = cors_default_enabled
        if cors_default_origins is not UNSET:
            field_dict["cors_default_origins"] = cors_default_origins

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.public_auth_block import PublicAuthBlock
        from ..models.scaling_policy import ScalingPolicy
        from ..models.service_replicas import ServiceReplicas

        d = dict(src_dict)

        def _parse_ram_mb(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ram_mb = _parse_ram_mb(d.pop("ram_mb", UNSET))

        def _parse_cpu_millicores(
            data: object,
        ) -> (
            None
            | Unset
            | UpdateAppRequestCpuMillicoresType1
            | UpdateAppRequestCpuMillicoresType2Type1
            | UpdateAppRequestCpuMillicoresType3Type1
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, int):
                    raise TypeError()
                cpu_millicores_type_1 = check_update_app_request_cpu_millicores_type_1(data)

                return cpu_millicores_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, int):
                    raise TypeError()
                cpu_millicores_type_2_type_1 = check_update_app_request_cpu_millicores_type_2_type_1(data)

                return cpu_millicores_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, int):
                    raise TypeError()
                cpu_millicores_type_3_type_1 = check_update_app_request_cpu_millicores_type_3_type_1(data)

                return cpu_millicores_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                None
                | Unset
                | UpdateAppRequestCpuMillicoresType1
                | UpdateAppRequestCpuMillicoresType2Type1
                | UpdateAppRequestCpuMillicoresType3Type1,
                data,
            )

        cpu_millicores = _parse_cpu_millicores(d.pop("cpu_millicores", UNSET))

        def _parse_idle_timeout_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        idle_timeout_s = _parse_idle_timeout_s(d.pop("idle_timeout_s", UNSET))

        def _parse_max_concurrency(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_concurrency = _parse_max_concurrency(d.pop("max_concurrency", UNSET))

        def _parse_execution_mode(
            data: object,
        ) -> (
            None
            | Unset
            | UpdateAppRequestExecutionModeType1
            | UpdateAppRequestExecutionModeType2Type1
            | UpdateAppRequestExecutionModeType3Type1
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_1 = check_update_app_request_execution_mode_type_1(data)

                return execution_mode_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_2_type_1 = check_update_app_request_execution_mode_type_2_type_1(data)

                return execution_mode_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_3_type_1 = check_update_app_request_execution_mode_type_3_type_1(data)

                return execution_mode_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                None
                | Unset
                | UpdateAppRequestExecutionModeType1
                | UpdateAppRequestExecutionModeType2Type1
                | UpdateAppRequestExecutionModeType3Type1,
                data,
            )

        execution_mode = _parse_execution_mode(d.pop("execution_mode", UNSET))

        def _parse_restart_policy(
            data: object,
        ) -> (
            None
            | Unset
            | UpdateAppRequestRestartPolicyType1
            | UpdateAppRequestRestartPolicyType2Type1
            | UpdateAppRequestRestartPolicyType3Type1
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restart_policy_type_1 = check_update_app_request_restart_policy_type_1(data)

                return restart_policy_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restart_policy_type_2_type_1 = check_update_app_request_restart_policy_type_2_type_1(data)

                return restart_policy_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restart_policy_type_3_type_1 = check_update_app_request_restart_policy_type_3_type_1(data)

                return restart_policy_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                None
                | Unset
                | UpdateAppRequestRestartPolicyType1
                | UpdateAppRequestRestartPolicyType2Type1
                | UpdateAppRequestRestartPolicyType3Type1,
                data,
            )

        restart_policy = _parse_restart_policy(d.pop("restart_policy", UNSET))

        def _parse_startup_deadline_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        startup_deadline_s = _parse_startup_deadline_s(d.pop("startup_deadline_s", UNSET))

        def _parse_max_retries(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_retries = _parse_max_retries(d.pop("max_retries", UNSET))

        _service_replicas = d.pop("service_replicas", UNSET)
        service_replicas: ServiceReplicas | Unset
        if isinstance(_service_replicas, Unset):
            service_replicas = UNSET
        else:
            service_replicas = ServiceReplicas.from_dict(_service_replicas)

        def _parse_min_instances(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        min_instances = _parse_min_instances(d.pop("min_instances", UNSET))

        egress_allowlist = cast(list[str], d.pop("egress_allowlist", UNSET))

        def _parse_autoscale_target_rps(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        autoscale_target_rps = _parse_autoscale_target_rps(d.pop("autoscale_target_rps", UNSET))

        def _parse_autoscale_target_cpu_pct(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        autoscale_target_cpu_pct = _parse_autoscale_target_cpu_pct(d.pop("autoscale_target_cpu_pct", UNSET))

        def _parse_streaming_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        streaming_enabled = _parse_streaming_enabled(d.pop("streaming_enabled", UNSET))

        def _parse_websocket_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        websocket_enabled = _parse_websocket_enabled(d.pop("websocket_enabled", UNSET))

        def _parse_route_metrics_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        route_metrics_enabled = _parse_route_metrics_enabled(d.pop("route_metrics_enabled", UNSET))

        def _parse_maintenance_mode(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        maintenance_mode = _parse_maintenance_mode(d.pop("maintenance_mode", UNSET))

        _app_protocol = d.pop("app_protocol", UNSET)
        app_protocol: UpdateAppRequestAppProtocol | Unset
        if isinstance(_app_protocol, Unset):
            app_protocol = UNSET
        else:
            app_protocol = check_update_app_request_app_protocol(_app_protocol)

        def _parse_scaling_policy(data: object) -> None | ScalingPolicy | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                scaling_policy_type_1 = ScalingPolicy.from_dict(data)

                return scaling_policy_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ScalingPolicy | Unset, data)

        scaling_policy = _parse_scaling_policy(d.pop("scaling_policy", UNSET))

        def _parse_require_signed(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        require_signed = _parse_require_signed(d.pop("require_signed", UNSET))

        def _parse_warm_snapshot_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        warm_snapshot_enabled = _parse_warm_snapshot_enabled(d.pop("warm_snapshot_enabled", UNSET))

        def _parse_warm_snapshot_min_requests(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        warm_snapshot_min_requests = _parse_warm_snapshot_min_requests(d.pop("warm_snapshot_min_requests", UNSET))

        def _parse_warm_snapshot_min_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        warm_snapshot_min_ms = _parse_warm_snapshot_min_ms(d.pop("warm_snapshot_min_ms", UNSET))

        def _parse_eviction_priority(
            data: object,
        ) -> (
            None
            | Unset
            | UpdateAppRequestEvictionPriorityType1
            | UpdateAppRequestEvictionPriorityType2Type1
            | UpdateAppRequestEvictionPriorityType3Type1
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                eviction_priority_type_1 = check_update_app_request_eviction_priority_type_1(data)

                return eviction_priority_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                eviction_priority_type_2_type_1 = check_update_app_request_eviction_priority_type_2_type_1(data)

                return eviction_priority_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                eviction_priority_type_3_type_1 = check_update_app_request_eviction_priority_type_3_type_1(data)

                return eviction_priority_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                None
                | Unset
                | UpdateAppRequestEvictionPriorityType1
                | UpdateAppRequestEvictionPriorityType2Type1
                | UpdateAppRequestEvictionPriorityType3Type1,
                data,
            )

        eviction_priority = _parse_eviction_priority(d.pop("eviction_priority", UNSET))

        def _parse_require_authn(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        require_authn = _parse_require_authn(d.pop("require_authn", UNSET))

        def _parse_public_auth(data: object) -> None | PublicAuthBlock | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                public_auth_type_1 = PublicAuthBlock.from_dict(data)

                return public_auth_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | PublicAuthBlock | Unset, data)

        public_auth = _parse_public_auth(d.pop("public_auth", UNSET))

        def _parse_overflow_node(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        overflow_node = _parse_overflow_node(d.pop("overflow_node", UNSET))

        def _parse_cors_default_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        cors_default_enabled = _parse_cors_default_enabled(d.pop("cors_default_enabled", UNSET))

        cors_default_origins = cast(list[str], d.pop("cors_default_origins", UNSET))

        update_app_request = cls(
            ram_mb=ram_mb,
            cpu_millicores=cpu_millicores,
            idle_timeout_s=idle_timeout_s,
            max_concurrency=max_concurrency,
            execution_mode=execution_mode,
            restart_policy=restart_policy,
            startup_deadline_s=startup_deadline_s,
            max_retries=max_retries,
            service_replicas=service_replicas,
            min_instances=min_instances,
            egress_allowlist=egress_allowlist,
            autoscale_target_rps=autoscale_target_rps,
            autoscale_target_cpu_pct=autoscale_target_cpu_pct,
            streaming_enabled=streaming_enabled,
            websocket_enabled=websocket_enabled,
            route_metrics_enabled=route_metrics_enabled,
            maintenance_mode=maintenance_mode,
            app_protocol=app_protocol,
            scaling_policy=scaling_policy,
            require_signed=require_signed,
            warm_snapshot_enabled=warm_snapshot_enabled,
            warm_snapshot_min_requests=warm_snapshot_min_requests,
            warm_snapshot_min_ms=warm_snapshot_min_ms,
            eviction_priority=eviction_priority,
            require_authn=require_authn,
            public_auth=public_auth,
            overflow_node=overflow_node,
            cors_default_enabled=cors_default_enabled,
            cors_default_origins=cors_default_origins,
        )

        update_app_request.additional_properties = d
        return update_app_request

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
