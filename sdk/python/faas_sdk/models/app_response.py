from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_response_app_protocol import AppResponseAppProtocol, check_app_response_app_protocol
from ..models.app_response_cpu_millicores import AppResponseCpuMillicores, check_app_response_cpu_millicores
from ..models.app_response_eviction_priority import AppResponseEvictionPriority, check_app_response_eviction_priority
from ..models.app_response_runtime import AppResponseRuntime, check_app_response_runtime
from ..models.app_response_type import AppResponseType, check_app_response_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_configured_resources import AppConfiguredResources
    from ..models.app_effective_limits import AppEffectiveLimits
    from ..models.app_manifest import AppManifest
    from ..models.parked_deployment_ref import ParkedDeploymentRef
    from ..models.public_auth_status import PublicAuthStatus
    from ..models.scaling_policy import ScalingPolicy


T = TypeVar("T", bound="AppResponse")


@_attrs_define
class AppResponse:
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy pointer, per-
    app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169 / #172).

    """

    id: str
    slug: str
    type_: AppResponseType
    ram_mb: int
    cpu_millicores: AppResponseCpuMillicores
    configured_resources: AppConfiguredResources
    """The memory and sustained CPU shape selected for each instance of this app."""
    max_concurrency: int
    concurrency_per_vm: int
    effective_limits: AppEffectiveLimits
    """The resource, scaling, rate, and timeout envelope currently applied to an app. Values are resolved from the
    app configuration and current plan; they describe enforcement rather than guest hardware alone."""
    min_instances: int
    status: str
    url: str
    manifest: AppManifest
    """App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-
    as-source flag (§ux 6.3). The optional `env_secrets` field carries sealed-secret refs ("secret:NAME" strings)
    resolved by the host at wake time against the app_secrets table (issue #460 / ADR-053 §Decision 1). Values are
    NEVER sealed ciphertext — only refs. M-1 (ADR-136) widens the contract additively with `healthcheck`,
    `stop_signal`, `stop_grace_period` from the OCI image-config spec; old guest-init ignores unknown fields per
    JSON semantics, so the widen is wire-compatible. M-2 (ADR-137 + ADR-138) widens additively with
    `execution_mode`, `restart_policy`, `startup_deadline_s`, `max_retries`, and `service_replicas` — these govern
    the lifecycle contract (request vs service vs worker vs job) and the per-mode replica scaffold. Defaults
    preserve today's behaviour (execution_mode=request, restart_policy=on-failure)."""
    autoscale_target_rps: int
    """Per-instance RPS target for the reactive scale-up trigger. 0 = disabled. Hobby/Pro/Scale only. When measured
    per-instance RPS exceeds this value, schedd admits another instance (up to max_concurrency). See ADR-037."""
    autoscale_target_cpu_pct: int
    """Per-instance CPU% target (1..100) for the reactive scale-up trigger. 0 = disabled. Pro/Scale only. When
    measured per-instance CPU% exceeds this value, schedd admits another instance (up to max_concurrency). See
    ADR-037."""
    runtime: AppResponseRuntime | Unset = UNSET
    """Runtime for `type: function` apps. Omit for `type: app` (the default)."""
    idle_timeout_s: int | None | Unset = UNSET
    egress_allowlist: list[str] | Unset = UNSET
    """Per-app outbound CIDR allowlist (ADR-031 + ADR-032). Each entry is a CIDR string — v4 (`1.2.3.0/24`) or v6
    (`2001:db8::/32`). v4-mapped v6 form (`::ffff:1.2.3.0/120`) is silently canonicalised to its v4 form at write
    time. Empty array means no allowlist rule; the per-netns chain's default-accept policy applies."""
    streaming_enabled: bool | Unset = UNSET
    """Per-app streaming flag (issue #471). Free customers always see this as false; Hobby/Pro/Scale can PATCH it.
    PR-B activates the streamed response path; PR-A only persists the flag."""
    websocket_enabled: bool | Unset = UNSET
    """Per-app raw-bytes Upgrade bridge flag (issue #676 / ADR-080). Default-on for Hobby/Pro/Scale; Free customers
    always see this as false. PATCH-true on Free is rejected by apid with 403 plan_websocket_not_allowed."""
    route_metrics_enabled: bool | Unset = UNSET
    """Per-app per-route observability flag (ADR-093). When true, gatewayd-internal emits
    gateway_request_duration_seconds{app,route,class} and serves the bounded reader at GET /v1/apps/{slug}/routes.
    Default-on for Hobby/Pro/Scale; Free customers always see this as false. PATCH-true on Free is rejected by apid
    with 403 plan_route_metrics_not_allowed."""
    maintenance_mode: bool | Unset = UNSET
    """Coarse per-app maintenance toggle (ADR-091 amendment). When true the gatewayd-internal hot-path short-
    circuits every request to this app with 503 + Retry-After (default 60 s) BEFORE auth, BEFORE wake, BEFORE any
    kind=maintenance edge rule. Free-tier allowed. Surfaced in the GET /v1/apps/{slug} response so dashboards can
    show 'maintenance on / off' alongside the streaming/WS pills."""
    scaling_policy: None | ScalingPolicy | Unset = UNSET
    """Per-app scaling policy (issue #462 / ADR-058). null = legacy row, project the empty-policy shape from
    min_instances / max_concurrency. Non-null = customer-authored policy persisted to the jsonb column
    `apps.scaling_policy`."""
    last_scale_out_at: datetime.datetime | None | Unset = UNSET
    """RFC 3339 timestamp of the most recent scale-out event schedd admitted for this app, or null if the app has
    never scaled out."""
    last_scale_in_at: datetime.datetime | None | Unset = UNSET
    """RFC 3339 timestamp of the most recent scale-in event schedd reaped for this app, or null if the app has
    never scaled in."""
    require_signed: bool | Unset = UNSET
    """Per-app cosign signature-enforcement flag (issue #472 / ADR-054). When true, OCI image deploys must carry a
    valid signature from a publisher in the per-app trusted_signers list. Default false."""
    warm_snapshot_enabled: bool | Unset = UNSET
    """Per-app two-tier snapshot flag (issue #470 / ADR-055). True on Pro/Scale by default; Free/Hobby always
    false."""
    warm_snapshot_min_requests: int | Unset = UNSET
    """Effective per-app request-count threshold for warm-tier capture on this app (issue #470 / ADR-055). Range
    [1, 100]."""
    warm_snapshot_min_ms: int | Unset = UNSET
    """Per-app time-since-first-ready threshold for warm-tier capture, milliseconds (issue #470 / ADR-055). Range
    [100, 60000]."""
    eviction_priority: AppResponseEvictionPriority | Unset = UNSET
    """Per-app eviction tier (issue #475). 'best_effort' (default) keeps the pre-#475 LRU-by-last_request_at reaper
    behaviour; 'reserved' protects the app from cross-account RAM-pressure eviction."""
    require_authn: bool | Unset = UNSET
    """Per-deployment token-gate flag (issue #560). When true, gatewayd-internal demands `Authorization: Bearer
    <token>` on every request; cross-account tokens receive 403 insufficient_scope. Pro/Scale only — Free/Hobby
    PATCH-true is rejected with 403 plan_require_authn_not_allowed."""
    parked_deployment: None | ParkedDeploymentRef | Unset = UNSET
    """Most-recently parked deployment for this app, or null if never parked (issue #554 / ADR-079 follow-up). The
    reference surfaces the closed-set parking reason + timestamp on GET /v1/apps/{slug} so operators can answer 'why
    is my app evicted_cold?' without grepping the audit log."""
    overflow_node: None | Unset | UUID = UNSET
    """Per-app preferred spill target for cross-node pressure rebalance (Tier A10 / ADR-088). Resolved UUID from
    the customer's named compute_nodes.name preference (null when unset). Consulted by Engine.RebalancePressuredApps
    before the A9 fallback; falls through to A9 when the target is inactive or full."""
    cors_default_enabled: bool | None | Unset = UNSET
    cors_default_origins: list[str] | Unset = UNSET
    public_auth: PublicAuthStatus | Unset = UNSET
    """Read-only per-app public-URL auth shape on AppResponse (issue #477 / ADR-077 + ADR-118). Mirrors the row
    contents without the plaintext credentials. The redaction posture is a load-bearing invariant — see ADR-077
    §Decision 're-redaction invariant': neither basic_user nor basic_pass is EVER returned on the wire, even when
    mode='basic'. To rotate credentials, the customer PATCHes a fresh public_auth block."""
    auth_default_flipped_at: datetime.datetime | None | Unset = UNSET
    app_protocol: AppResponseAppProtocol | Unset = UNSET
    """Per-app wire-protocol selector (ADR-124). Closed set {http1, http2, grpc}. Default 'http1' (universal).
    Setting 'grpc' is plan-gated to Hobby+/Pro/Scale; Free customers see this as 'http1'."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.parked_deployment_ref import ParkedDeploymentRef
        from ..models.scaling_policy import ScalingPolicy

        id = self.id

        slug = self.slug

        type_: str = self.type_

        ram_mb = self.ram_mb

        cpu_millicores: int = self.cpu_millicores

        configured_resources = self.configured_resources.to_dict()

        max_concurrency = self.max_concurrency

        concurrency_per_vm = self.concurrency_per_vm

        effective_limits = self.effective_limits.to_dict()

        min_instances = self.min_instances

        status = self.status

        url = self.url

        manifest = self.manifest.to_dict()

        autoscale_target_rps = self.autoscale_target_rps

        autoscale_target_cpu_pct = self.autoscale_target_cpu_pct

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        idle_timeout_s: int | None | Unset
        if isinstance(self.idle_timeout_s, Unset):
            idle_timeout_s = UNSET
        else:
            idle_timeout_s = self.idle_timeout_s

        egress_allowlist: list[str] | Unset = UNSET
        if not isinstance(self.egress_allowlist, Unset):
            egress_allowlist = self.egress_allowlist

        streaming_enabled = self.streaming_enabled

        websocket_enabled = self.websocket_enabled

        route_metrics_enabled = self.route_metrics_enabled

        maintenance_mode = self.maintenance_mode

        scaling_policy: dict[str, Any] | None | Unset
        if isinstance(self.scaling_policy, Unset):
            scaling_policy = UNSET
        elif isinstance(self.scaling_policy, ScalingPolicy):
            scaling_policy = self.scaling_policy.to_dict()
        else:
            scaling_policy = self.scaling_policy

        last_scale_out_at: None | str | Unset
        if isinstance(self.last_scale_out_at, Unset):
            last_scale_out_at = UNSET
        elif isinstance(self.last_scale_out_at, datetime.datetime):
            last_scale_out_at = self.last_scale_out_at.isoformat()
        else:
            last_scale_out_at = self.last_scale_out_at

        last_scale_in_at: None | str | Unset
        if isinstance(self.last_scale_in_at, Unset):
            last_scale_in_at = UNSET
        elif isinstance(self.last_scale_in_at, datetime.datetime):
            last_scale_in_at = self.last_scale_in_at.isoformat()
        else:
            last_scale_in_at = self.last_scale_in_at

        require_signed = self.require_signed

        warm_snapshot_enabled = self.warm_snapshot_enabled

        warm_snapshot_min_requests = self.warm_snapshot_min_requests

        warm_snapshot_min_ms = self.warm_snapshot_min_ms

        eviction_priority: str | Unset = UNSET
        if not isinstance(self.eviction_priority, Unset):
            eviction_priority = self.eviction_priority

        require_authn = self.require_authn

        parked_deployment: dict[str, Any] | None | Unset
        if isinstance(self.parked_deployment, Unset):
            parked_deployment = UNSET
        elif isinstance(self.parked_deployment, ParkedDeploymentRef):
            parked_deployment = self.parked_deployment.to_dict()
        else:
            parked_deployment = self.parked_deployment

        overflow_node: None | str | Unset
        if isinstance(self.overflow_node, Unset):
            overflow_node = UNSET
        elif isinstance(self.overflow_node, UUID):
            overflow_node = str(self.overflow_node)
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

        public_auth: dict[str, Any] | Unset = UNSET
        if not isinstance(self.public_auth, Unset):
            public_auth = self.public_auth.to_dict()

        auth_default_flipped_at: None | str | Unset
        if isinstance(self.auth_default_flipped_at, Unset):
            auth_default_flipped_at = UNSET
        elif isinstance(self.auth_default_flipped_at, datetime.datetime):
            auth_default_flipped_at = self.auth_default_flipped_at.isoformat()
        else:
            auth_default_flipped_at = self.auth_default_flipped_at

        app_protocol: str | Unset = UNSET
        if not isinstance(self.app_protocol, Unset):
            app_protocol = self.app_protocol

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "slug": slug,
                "type": type_,
                "ram_mb": ram_mb,
                "cpu_millicores": cpu_millicores,
                "configured_resources": configured_resources,
                "max_concurrency": max_concurrency,
                "concurrency_per_vm": concurrency_per_vm,
                "effective_limits": effective_limits,
                "min_instances": min_instances,
                "status": status,
                "url": url,
                "manifest": manifest,
                "autoscale_target_rps": autoscale_target_rps,
                "autoscale_target_cpu_pct": autoscale_target_cpu_pct,
            }
        )
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if idle_timeout_s is not UNSET:
            field_dict["idle_timeout_s"] = idle_timeout_s
        if egress_allowlist is not UNSET:
            field_dict["egress_allowlist"] = egress_allowlist
        if streaming_enabled is not UNSET:
            field_dict["streaming_enabled"] = streaming_enabled
        if websocket_enabled is not UNSET:
            field_dict["websocket_enabled"] = websocket_enabled
        if route_metrics_enabled is not UNSET:
            field_dict["route_metrics_enabled"] = route_metrics_enabled
        if maintenance_mode is not UNSET:
            field_dict["maintenance_mode"] = maintenance_mode
        if scaling_policy is not UNSET:
            field_dict["scaling_policy"] = scaling_policy
        if last_scale_out_at is not UNSET:
            field_dict["last_scale_out_at"] = last_scale_out_at
        if last_scale_in_at is not UNSET:
            field_dict["last_scale_in_at"] = last_scale_in_at
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
        if parked_deployment is not UNSET:
            field_dict["parked_deployment"] = parked_deployment
        if overflow_node is not UNSET:
            field_dict["overflow_node"] = overflow_node
        if cors_default_enabled is not UNSET:
            field_dict["cors_default_enabled"] = cors_default_enabled
        if cors_default_origins is not UNSET:
            field_dict["cors_default_origins"] = cors_default_origins
        if public_auth is not UNSET:
            field_dict["public_auth"] = public_auth
        if auth_default_flipped_at is not UNSET:
            field_dict["auth_default_flipped_at"] = auth_default_flipped_at
        if app_protocol is not UNSET:
            field_dict["app_protocol"] = app_protocol

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_configured_resources import AppConfiguredResources
        from ..models.app_effective_limits import AppEffectiveLimits
        from ..models.app_manifest import AppManifest
        from ..models.parked_deployment_ref import ParkedDeploymentRef
        from ..models.public_auth_status import PublicAuthStatus
        from ..models.scaling_policy import ScalingPolicy

        d = dict(src_dict)
        id = d.pop("id")

        slug = d.pop("slug")

        type_ = check_app_response_type(d.pop("type"))

        ram_mb = d.pop("ram_mb")

        cpu_millicores = check_app_response_cpu_millicores(d.pop("cpu_millicores"))

        configured_resources = AppConfiguredResources.from_dict(d.pop("configured_resources"))

        max_concurrency = d.pop("max_concurrency")

        concurrency_per_vm = d.pop("concurrency_per_vm")

        effective_limits = AppEffectiveLimits.from_dict(d.pop("effective_limits"))

        min_instances = d.pop("min_instances")

        status = d.pop("status")

        url = d.pop("url")

        manifest = AppManifest.from_dict(d.pop("manifest"))

        autoscale_target_rps = d.pop("autoscale_target_rps")

        autoscale_target_cpu_pct = d.pop("autoscale_target_cpu_pct")

        _runtime = d.pop("runtime", UNSET)
        runtime: AppResponseRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_app_response_runtime(_runtime)

        def _parse_idle_timeout_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        idle_timeout_s = _parse_idle_timeout_s(d.pop("idle_timeout_s", UNSET))

        egress_allowlist = cast(list[str], d.pop("egress_allowlist", UNSET))

        streaming_enabled = d.pop("streaming_enabled", UNSET)

        websocket_enabled = d.pop("websocket_enabled", UNSET)

        route_metrics_enabled = d.pop("route_metrics_enabled", UNSET)

        maintenance_mode = d.pop("maintenance_mode", UNSET)

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

        def _parse_last_scale_out_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_scale_out_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_scale_out_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_scale_out_at = _parse_last_scale_out_at(d.pop("last_scale_out_at", UNSET))

        def _parse_last_scale_in_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_scale_in_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_scale_in_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_scale_in_at = _parse_last_scale_in_at(d.pop("last_scale_in_at", UNSET))

        require_signed = d.pop("require_signed", UNSET)

        warm_snapshot_enabled = d.pop("warm_snapshot_enabled", UNSET)

        warm_snapshot_min_requests = d.pop("warm_snapshot_min_requests", UNSET)

        warm_snapshot_min_ms = d.pop("warm_snapshot_min_ms", UNSET)

        _eviction_priority = d.pop("eviction_priority", UNSET)
        eviction_priority: AppResponseEvictionPriority | Unset
        if isinstance(_eviction_priority, Unset):
            eviction_priority = UNSET
        else:
            eviction_priority = check_app_response_eviction_priority(_eviction_priority)

        require_authn = d.pop("require_authn", UNSET)

        def _parse_parked_deployment(data: object) -> None | ParkedDeploymentRef | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                parked_deployment_type_0 = ParkedDeploymentRef.from_dict(data)

                return parked_deployment_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ParkedDeploymentRef | Unset, data)

        parked_deployment = _parse_parked_deployment(d.pop("parked_deployment", UNSET))

        def _parse_overflow_node(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                overflow_node_type_0 = UUID(data)

                return overflow_node_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        overflow_node = _parse_overflow_node(d.pop("overflow_node", UNSET))

        def _parse_cors_default_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        cors_default_enabled = _parse_cors_default_enabled(d.pop("cors_default_enabled", UNSET))

        cors_default_origins = cast(list[str], d.pop("cors_default_origins", UNSET))

        _public_auth = d.pop("public_auth", UNSET)
        public_auth: PublicAuthStatus | Unset
        if isinstance(_public_auth, Unset):
            public_auth = UNSET
        else:
            public_auth = PublicAuthStatus.from_dict(_public_auth)

        def _parse_auth_default_flipped_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                auth_default_flipped_at_type_0 = datetime.datetime.fromisoformat(data)

                return auth_default_flipped_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        auth_default_flipped_at = _parse_auth_default_flipped_at(d.pop("auth_default_flipped_at", UNSET))

        _app_protocol = d.pop("app_protocol", UNSET)
        app_protocol: AppResponseAppProtocol | Unset
        if isinstance(_app_protocol, Unset):
            app_protocol = UNSET
        else:
            app_protocol = check_app_response_app_protocol(_app_protocol)

        app_response = cls(
            id=id,
            slug=slug,
            type_=type_,
            ram_mb=ram_mb,
            cpu_millicores=cpu_millicores,
            configured_resources=configured_resources,
            max_concurrency=max_concurrency,
            concurrency_per_vm=concurrency_per_vm,
            effective_limits=effective_limits,
            min_instances=min_instances,
            status=status,
            url=url,
            manifest=manifest,
            autoscale_target_rps=autoscale_target_rps,
            autoscale_target_cpu_pct=autoscale_target_cpu_pct,
            runtime=runtime,
            idle_timeout_s=idle_timeout_s,
            egress_allowlist=egress_allowlist,
            streaming_enabled=streaming_enabled,
            websocket_enabled=websocket_enabled,
            route_metrics_enabled=route_metrics_enabled,
            maintenance_mode=maintenance_mode,
            scaling_policy=scaling_policy,
            last_scale_out_at=last_scale_out_at,
            last_scale_in_at=last_scale_in_at,
            require_signed=require_signed,
            warm_snapshot_enabled=warm_snapshot_enabled,
            warm_snapshot_min_requests=warm_snapshot_min_requests,
            warm_snapshot_min_ms=warm_snapshot_min_ms,
            eviction_priority=eviction_priority,
            require_authn=require_authn,
            parked_deployment=parked_deployment,
            overflow_node=overflow_node,
            cors_default_enabled=cors_default_enabled,
            cors_default_origins=cors_default_origins,
            public_auth=public_auth,
            auth_default_flipped_at=auth_default_flipped_at,
            app_protocol=app_protocol,
        )

        app_response.additional_properties = d
        return app_response

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
