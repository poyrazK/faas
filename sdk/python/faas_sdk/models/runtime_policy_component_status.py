from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.runtime_policy_component_status_scope import (
    RuntimePolicyComponentStatusScope,
    check_runtime_policy_component_status_scope,
)
from ..models.runtime_policy_component_status_state import (
    RuntimePolicyComponentStatusState,
    check_runtime_policy_component_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RuntimePolicyComponentStatus")


@_attrs_define
class RuntimePolicyComponentStatus:
    """Fresh serving-gateway status for a policy component with explicit scope. Its desired revision is meaningful within
    that component's ledger or filtered projection.

    """

    desired_revision: int
    state: RuntimePolicyComponentStatusState
    serving_gateways: int
    applied_gateways: int
    pending_gateways: int
    stale_gateways: int
    scope: RuntimePolicyComponentStatusScope | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        desired_revision = self.desired_revision

        state: str = self.state

        serving_gateways = self.serving_gateways

        applied_gateways = self.applied_gateways

        pending_gateways = self.pending_gateways

        stale_gateways = self.stale_gateways

        scope: str | Unset = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "desired_revision": desired_revision,
                "state": state,
                "serving_gateways": serving_gateways,
                "applied_gateways": applied_gateways,
                "pending_gateways": pending_gateways,
                "stale_gateways": stale_gateways,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        desired_revision = d.pop("desired_revision")

        state = check_runtime_policy_component_status_state(d.pop("state"))

        serving_gateways = d.pop("serving_gateways")

        applied_gateways = d.pop("applied_gateways")

        pending_gateways = d.pop("pending_gateways")

        stale_gateways = d.pop("stale_gateways")

        _scope = d.pop("scope", UNSET)
        scope: RuntimePolicyComponentStatusScope | Unset
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = check_runtime_policy_component_status_scope(_scope)

        runtime_policy_component_status = cls(
            desired_revision=desired_revision,
            state=state,
            serving_gateways=serving_gateways,
            applied_gateways=applied_gateways,
            pending_gateways=pending_gateways,
            stale_gateways=stale_gateways,
            scope=scope,
        )

        runtime_policy_component_status.additional_properties = d
        return runtime_policy_component_status

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
