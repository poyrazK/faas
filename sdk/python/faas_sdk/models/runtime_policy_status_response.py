from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.runtime_policy_status_response_state import (
    RuntimePolicyStatusResponseState,
    check_runtime_policy_status_response_state,
)

if TYPE_CHECKING:
    from ..models.runtime_policy_component_status import RuntimePolicyComponentStatus


T = TypeVar("T", bound="RuntimePolicyStatusResponse")


@_attrs_define
class RuntimePolicyStatusResponse:
    """Fresh serving-gateway status for app-cache and deployment traffic changes. The edge_rules component reports its
    separate revision sequence.

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
    edge_rules: RuntimePolicyComponentStatus
    """Fresh serving-gateway application status for a policy ledger with its own revision sequence."""
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

        edge_rules = self.edge_rules.to_dict()

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
                "edge_rules": edge_rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.runtime_policy_component_status import RuntimePolicyComponentStatus

        d = dict(src_dict)
        app_id = d.pop("app_id")

        desired_revision = d.pop("desired_revision")

        state = check_runtime_policy_status_response_state(d.pop("state"))

        coverage = cast(list[str], d.pop("coverage"))

        serving_gateways = d.pop("serving_gateways")

        applied_gateways = d.pop("applied_gateways")

        pending_gateways = d.pop("pending_gateways")

        stale_gateways = d.pop("stale_gateways")

        edge_rules = RuntimePolicyComponentStatus.from_dict(d.pop("edge_rules"))

        runtime_policy_status_response = cls(
            app_id=app_id,
            desired_revision=desired_revision,
            state=state,
            coverage=coverage,
            serving_gateways=serving_gateways,
            applied_gateways=applied_gateways,
            pending_gateways=pending_gateways,
            stale_gateways=stale_gateways,
            edge_rules=edge_rules,
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
