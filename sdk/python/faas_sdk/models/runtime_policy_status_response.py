from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.runtime_policy_status_response_state import (
    RuntimePolicyStatusResponseState,
    check_runtime_policy_status_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.runtime_policy_component_status import RuntimePolicyComponentStatus
    from ..models.runtime_policy_node_status import RuntimePolicyNodeStatus
    from ..models.runtime_policy_scheduler_status import RuntimePolicySchedulerStatus
    from ..models.traffic_runtime_status import TrafficRuntimeStatus


T = TypeVar("T", bound="RuntimePolicyStatusResponse")


@_attrs_define
class RuntimePolicyStatusResponse:
    """Runtime policy status across gateway replicas, the owning scheduler, and live VM consumers. Each component reports
    its scoped desired revision. Gateway request policy filters app-row changes from the combined app-cache and traffic
    projection. Gateway acknowledgements require the current process generation and a fresh serving report. Traffic
    runtime reports wiring independently of convergence and enforcement; older API servers may omit it.

    """

    app_id: str
    desired_revision: int
    """Latest desired control-plane revision for app-cache and deployment traffic policy."""
    state: RuntimePolicyStatusResponseState
    coverage: list[str]
    serving_gateways: int
    applied_gateways: int
    pending_gateways: int
    stale_gateways: int
    request_policy: RuntimePolicyComponentStatus
    """Fresh serving-gateway status for a policy component with explicit scope. Its desired revision is meaningful
    within that component's ledger or filtered projection."""
    edge_rules: RuntimePolicyComponentStatus
    """Fresh serving-gateway status for a policy component with explicit scope. Its desired revision is meaningful
    within that component's ledger or filtered projection."""
    cors_presets: RuntimePolicyComponentStatus
    """Fresh serving-gateway status for a policy component with explicit scope. Its desired revision is meaningful
    within that component's ledger or filtered projection."""
    response_cache: RuntimePolicyComponentStatus
    """Fresh serving-gateway status for a policy component with explicit scope. Its desired revision is meaningful
    within that component's ledger or filtered projection."""
    egress_allowlist: RuntimePolicyNodeStatus
    """Fresh app-level host policy status for compute nodes currently hosting live instances of this app. This
    shape is used by the egress_allowlist and cpu_limit components."""
    cpu_limit: RuntimePolicyNodeStatus
    """Fresh app-level host policy status for compute nodes currently hosting live instances of this app. This
    shape is used by the egress_allowlist and cpu_limit components."""
    scheduler_scaling: RuntimePolicySchedulerStatus
    """Fresh observation of the desired scaling policy by the owning schedd. Active means the policy was loaded,
    not that a metric-driven replica target has been reached."""
    traffic_runtime: TrafficRuntimeStatus | Unset = UNSET
    """Fresh compute gateway wiring observations for the active serving roster. Reports expire after 10 seconds;
    replacement, missing reports and readiness failure remove observations. Wiring does not prove backend health or
    request enforcement. Node identities and process tokens are not exposed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        desired_revision = self.desired_revision

        state: str = self.state

        coverage = self.coverage

        serving_gateways = self.serving_gateways

        applied_gateways = self.applied_gateways

        pending_gateways = self.pending_gateways

        stale_gateways = self.stale_gateways

        request_policy = self.request_policy.to_dict()

        edge_rules = self.edge_rules.to_dict()

        cors_presets = self.cors_presets.to_dict()

        response_cache = self.response_cache.to_dict()

        egress_allowlist = self.egress_allowlist.to_dict()

        cpu_limit = self.cpu_limit.to_dict()

        scheduler_scaling = self.scheduler_scaling.to_dict()

        traffic_runtime: dict[str, Any] | Unset = UNSET
        if not isinstance(self.traffic_runtime, Unset):
            traffic_runtime = self.traffic_runtime.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "desired_revision": desired_revision,
                "state": state,
                "coverage": coverage,
                "serving_gateways": serving_gateways,
                "applied_gateways": applied_gateways,
                "pending_gateways": pending_gateways,
                "stale_gateways": stale_gateways,
                "request_policy": request_policy,
                "edge_rules": edge_rules,
                "cors_presets": cors_presets,
                "response_cache": response_cache,
                "egress_allowlist": egress_allowlist,
                "cpu_limit": cpu_limit,
                "scheduler_scaling": scheduler_scaling,
            }
        )
        if traffic_runtime is not UNSET:
            field_dict["traffic_runtime"] = traffic_runtime

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.runtime_policy_component_status import RuntimePolicyComponentStatus
        from ..models.runtime_policy_node_status import RuntimePolicyNodeStatus
        from ..models.runtime_policy_scheduler_status import RuntimePolicySchedulerStatus
        from ..models.traffic_runtime_status import TrafficRuntimeStatus

        d = dict(src_dict)
        app_id = d.pop("app_id")

        desired_revision = d.pop("desired_revision")

        state = check_runtime_policy_status_response_state(d.pop("state"))

        coverage = cast(list[str], d.pop("coverage"))

        serving_gateways = d.pop("serving_gateways")

        applied_gateways = d.pop("applied_gateways")

        pending_gateways = d.pop("pending_gateways")

        stale_gateways = d.pop("stale_gateways")

        request_policy = RuntimePolicyComponentStatus.from_dict(d.pop("request_policy"))

        edge_rules = RuntimePolicyComponentStatus.from_dict(d.pop("edge_rules"))

        cors_presets = RuntimePolicyComponentStatus.from_dict(d.pop("cors_presets"))

        response_cache = RuntimePolicyComponentStatus.from_dict(d.pop("response_cache"))

        egress_allowlist = RuntimePolicyNodeStatus.from_dict(d.pop("egress_allowlist"))

        cpu_limit = RuntimePolicyNodeStatus.from_dict(d.pop("cpu_limit"))

        scheduler_scaling = RuntimePolicySchedulerStatus.from_dict(d.pop("scheduler_scaling"))

        _traffic_runtime = d.pop("traffic_runtime", UNSET)
        traffic_runtime: TrafficRuntimeStatus | Unset
        if isinstance(_traffic_runtime, Unset):
            traffic_runtime = UNSET
        else:
            traffic_runtime = TrafficRuntimeStatus.from_dict(_traffic_runtime)

        runtime_policy_status_response = cls(
            app_id=app_id,
            desired_revision=desired_revision,
            state=state,
            coverage=coverage,
            serving_gateways=serving_gateways,
            applied_gateways=applied_gateways,
            pending_gateways=pending_gateways,
            stale_gateways=stale_gateways,
            request_policy=request_policy,
            edge_rules=edge_rules,
            cors_presets=cors_presets,
            response_cache=response_cache,
            egress_allowlist=egress_allowlist,
            cpu_limit=cpu_limit,
            scheduler_scaling=scheduler_scaling,
            traffic_runtime=traffic_runtime,
        )

        runtime_policy_status_response.additional_properties = d
        return runtime_policy_status_response

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
