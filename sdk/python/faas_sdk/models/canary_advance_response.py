from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_response import DeploymentResponse
    from ..models.route_gate_decision import RouteGateDecision
    from ..models.route_health_decision import RouteHealthDecision


T = TypeVar("T", bound="CanaryAdvanceResponse")


@_attrs_define
class CanaryAdvanceResponse:
    """The atomic canary transition result and the deployment_audit row id."""

    deployment: DeploymentResponse
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last
    deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed
    because the ref shape is non-secret by design."""
    audit_id: str
    """The deployment_audit row id, stringified for SDK portability."""
    route_health: RouteHealthDecision | Unset = UNSET
    """Metadata-only decision evaluated inside the canary traffic transaction; history_id correlates the exact
    saved evidence with the advance response and traffic audit."""
    route_gate: RouteGateDecision | Unset = UNSET
    """Metadata-only decision from the same transaction as a canary traffic advance. Findings remain in the route
    result API."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment = self.deployment.to_dict()

        audit_id = self.audit_id

        route_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_health, Unset):
            route_health = self.route_health.to_dict()

        route_gate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_gate, Unset):
            route_gate = self.route_gate.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment": deployment,
                "audit_id": audit_id,
            }
        )
        if route_health is not UNSET:
            field_dict["route_health"] = route_health
        if route_gate is not UNSET:
            field_dict["route_gate"] = route_gate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_response import DeploymentResponse
        from ..models.route_gate_decision import RouteGateDecision
        from ..models.route_health_decision import RouteHealthDecision

        d = dict(src_dict)
        deployment = DeploymentResponse.from_dict(d.pop("deployment"))

        audit_id = d.pop("audit_id")

        _route_health = d.pop("route_health", UNSET)
        route_health: RouteHealthDecision | Unset
        if isinstance(_route_health, Unset):
            route_health = UNSET
        else:
            route_health = RouteHealthDecision.from_dict(_route_health)

        _route_gate = d.pop("route_gate", UNSET)
        route_gate: RouteGateDecision | Unset
        if isinstance(_route_gate, Unset):
            route_gate = UNSET
        else:
            route_gate = RouteGateDecision.from_dict(_route_gate)

        canary_advance_response = cls(
            deployment=deployment,
            audit_id=audit_id,
            route_health=route_health,
            route_gate=route_gate,
        )

        canary_advance_response.additional_properties = d
        return canary_advance_response

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
