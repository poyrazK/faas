from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.traffic_runtime_status_enforcement_status import (
    TrafficRuntimeStatusEnforcementStatus,
    check_traffic_runtime_status_enforcement_status,
)
from ..models.traffic_runtime_status_scope import TrafficRuntimeStatusScope, check_traffic_runtime_status_scope
from ..models.traffic_runtime_status_state import TrafficRuntimeStatusState, check_traffic_runtime_status_state

if TYPE_CHECKING:
    from ..models.traffic_runtime_feature_status import TrafficRuntimeFeatureStatus


T = TypeVar("T", bound="TrafficRuntimeStatus")


@_attrs_define
class TrafficRuntimeStatus:
    """Fresh compute gateway wiring observations for the active serving roster. Reports expire after 10 seconds;
    replacement, missing reports and readiness failure remove observations. Wiring does not prove backend health or
    request enforcement. Node identities and process tokens are not exposed.

    """

    scope: TrafficRuntimeStatusScope
    state: TrafficRuntimeStatusState
    enforcement_status: TrafficRuntimeStatusEnforcementStatus
    serving_gateways: int
    fresh_gateways: int
    stale_gateways: int
    missing_gateways: int
    public_retry: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    rate_counter: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    retry_counter: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    deadline_signing: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    policy_snapshot: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    security_revocation: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    managed_http: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    managed_circuit: TrafficRuntimeFeatureStatus
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including
    shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        state: str = self.state

        enforcement_status: str = self.enforcement_status

        serving_gateways = self.serving_gateways

        fresh_gateways = self.fresh_gateways

        stale_gateways = self.stale_gateways

        missing_gateways = self.missing_gateways

        public_retry = self.public_retry.to_dict()

        rate_counter = self.rate_counter.to_dict()

        retry_counter = self.retry_counter.to_dict()

        deadline_signing = self.deadline_signing.to_dict()

        policy_snapshot = self.policy_snapshot.to_dict()

        security_revocation = self.security_revocation.to_dict()

        managed_http = self.managed_http.to_dict()

        managed_circuit = self.managed_circuit.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "state": state,
                "enforcement_status": enforcement_status,
                "serving_gateways": serving_gateways,
                "fresh_gateways": fresh_gateways,
                "stale_gateways": stale_gateways,
                "missing_gateways": missing_gateways,
                "public_retry": public_retry,
                "rate_counter": rate_counter,
                "retry_counter": retry_counter,
                "deadline_signing": deadline_signing,
                "policy_snapshot": policy_snapshot,
                "security_revocation": security_revocation,
                "managed_http": managed_http,
                "managed_circuit": managed_circuit,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.traffic_runtime_feature_status import TrafficRuntimeFeatureStatus

        d = dict(src_dict)
        scope = check_traffic_runtime_status_scope(d.pop("scope"))

        state = check_traffic_runtime_status_state(d.pop("state"))

        enforcement_status = check_traffic_runtime_status_enforcement_status(d.pop("enforcement_status"))

        serving_gateways = d.pop("serving_gateways")

        fresh_gateways = d.pop("fresh_gateways")

        stale_gateways = d.pop("stale_gateways")

        missing_gateways = d.pop("missing_gateways")

        public_retry = TrafficRuntimeFeatureStatus.from_dict(d.pop("public_retry"))

        rate_counter = TrafficRuntimeFeatureStatus.from_dict(d.pop("rate_counter"))

        retry_counter = TrafficRuntimeFeatureStatus.from_dict(d.pop("retry_counter"))

        deadline_signing = TrafficRuntimeFeatureStatus.from_dict(d.pop("deadline_signing"))

        policy_snapshot = TrafficRuntimeFeatureStatus.from_dict(d.pop("policy_snapshot"))

        security_revocation = TrafficRuntimeFeatureStatus.from_dict(d.pop("security_revocation"))

        managed_http = TrafficRuntimeFeatureStatus.from_dict(d.pop("managed_http"))

        managed_circuit = TrafficRuntimeFeatureStatus.from_dict(d.pop("managed_circuit"))

        traffic_runtime_status = cls(
            scope=scope,
            state=state,
            enforcement_status=enforcement_status,
            serving_gateways=serving_gateways,
            fresh_gateways=fresh_gateways,
            stale_gateways=stale_gateways,
            missing_gateways=missing_gateways,
            public_retry=public_retry,
            rate_counter=rate_counter,
            retry_counter=retry_counter,
            deadline_signing=deadline_signing,
            policy_snapshot=policy_snapshot,
            security_revocation=security_revocation,
            managed_http=managed_http,
            managed_circuit=managed_circuit,
        )

        traffic_runtime_status.additional_properties = d
        return traffic_runtime_status

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
