from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.runtime_policy_node_status_scope import (
    RuntimePolicyNodeStatusScope,
    check_runtime_policy_node_status_scope,
)
from ..models.runtime_policy_node_status_state import (
    RuntimePolicyNodeStatusState,
    check_runtime_policy_node_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RuntimePolicyNodeStatus")


@_attrs_define
class RuntimePolicyNodeStatus:
    """Fresh app-level host policy status for compute nodes currently hosting live instances of this app. This shape is
    used by the egress_allowlist and cpu_limit components.

    """

    desired_revision: int
    state: RuntimePolicyNodeStatusState
    serving_nodes: int
    applied_nodes: int
    pending_nodes: int
    stale_nodes: int
    scope: RuntimePolicyNodeStatusScope | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        desired_revision = self.desired_revision

        state: str = self.state

        serving_nodes = self.serving_nodes

        applied_nodes = self.applied_nodes

        pending_nodes = self.pending_nodes

        stale_nodes = self.stale_nodes

        scope: str | Unset = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "desired_revision": desired_revision,
                "state": state,
                "serving_nodes": serving_nodes,
                "applied_nodes": applied_nodes,
                "pending_nodes": pending_nodes,
                "stale_nodes": stale_nodes,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        desired_revision = d.pop("desired_revision")

        state = check_runtime_policy_node_status_state(d.pop("state"))

        serving_nodes = d.pop("serving_nodes")

        applied_nodes = d.pop("applied_nodes")

        pending_nodes = d.pop("pending_nodes")

        stale_nodes = d.pop("stale_nodes")

        _scope = d.pop("scope", UNSET)
        scope: RuntimePolicyNodeStatusScope | Unset
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = check_runtime_policy_node_status_scope(_scope)

        runtime_policy_node_status = cls(
            desired_revision=desired_revision,
            state=state,
            serving_nodes=serving_nodes,
            applied_nodes=applied_nodes,
            pending_nodes=pending_nodes,
            stale_nodes=stale_nodes,
            scope=scope,
        )

        runtime_policy_node_status.additional_properties = d
        return runtime_policy_node_status

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
