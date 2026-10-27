from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_policy_convergence_snapshot_state import (
    MCPPolicyConvergenceSnapshotState,
    check_mcp_policy_convergence_snapshot_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPPolicyConvergenceSnapshot")


@_attrs_define
class MCPPolicyConvergenceSnapshot:
    """Account-scoped live gateway replay observation for an active HTTP deployment. Snapshot metadata only; token
    enforcement is not attested. Correlates retained admission evidence when available.

    """

    state: MCPPolicyConvergenceSnapshotState
    captured_at: datetime.datetime
    desired_revision: int
    serving_gateways: int
    pending_gateways: int
    stale_gateways: int
    unconverged_gateways: int
    """Distinct gateways with stale/missing/future reports or pending replay."""
    deployment_id: UUID | Unset = UNSET
    admission_id: UUID | Unset = UNSET
    policy_status_path: str | Unset = UNSET
    admission_history_path: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        captured_at = self.captured_at.isoformat()

        desired_revision = self.desired_revision

        serving_gateways = self.serving_gateways

        pending_gateways = self.pending_gateways

        stale_gateways = self.stale_gateways

        unconverged_gateways = self.unconverged_gateways

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        admission_id: str | Unset = UNSET
        if not isinstance(self.admission_id, Unset):
            admission_id = str(self.admission_id)

        policy_status_path = self.policy_status_path

        admission_history_path = self.admission_history_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "captured_at": captured_at,
                "desired_revision": desired_revision,
                "serving_gateways": serving_gateways,
                "pending_gateways": pending_gateways,
                "stale_gateways": stale_gateways,
                "unconverged_gateways": unconverged_gateways,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if admission_id is not UNSET:
            field_dict["admission_id"] = admission_id
        if policy_status_path is not UNSET:
            field_dict["policy_status_path"] = policy_status_path
        if admission_history_path is not UNSET:
            field_dict["admission_history_path"] = admission_history_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_mcp_policy_convergence_snapshot_state(d.pop("state"))

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        desired_revision = d.pop("desired_revision")

        serving_gateways = d.pop("serving_gateways")

        pending_gateways = d.pop("pending_gateways")

        stale_gateways = d.pop("stale_gateways")

        unconverged_gateways = d.pop("unconverged_gateways")

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        _admission_id = d.pop("admission_id", UNSET)
        admission_id: UUID | Unset
        if isinstance(_admission_id, Unset):
            admission_id = UNSET
        else:
            admission_id = UUID(_admission_id)

        policy_status_path = d.pop("policy_status_path", UNSET)

        admission_history_path = d.pop("admission_history_path", UNSET)

        mcp_policy_convergence_snapshot = cls(
            state=state,
            captured_at=captured_at,
            desired_revision=desired_revision,
            serving_gateways=serving_gateways,
            pending_gateways=pending_gateways,
            stale_gateways=stale_gateways,
            unconverged_gateways=unconverged_gateways,
            deployment_id=deployment_id,
            admission_id=admission_id,
            policy_status_path=policy_status_path,
            admission_history_path=admission_history_path,
        )

        mcp_policy_convergence_snapshot.additional_properties = d
        return mcp_policy_convergence_snapshot

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
