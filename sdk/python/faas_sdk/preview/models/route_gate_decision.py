from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_gate_decision_mode import RouteGateDecisionMode, check_route_gate_decision_mode
from ..models.route_gate_decision_status import RouteGateDecisionStatus, check_route_gate_decision_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteGateDecision")


@_attrs_define
class RouteGateDecision:
    """Metadata-only decision from the same transaction as a canary traffic advance. Findings remain in the route result
    API.

    """

    mode: RouteGateDecisionMode
    revision: int
    deployment_id: UUID
    status: RouteGateDecisionStatus
    reasons: list[str]
    check_queued: bool
    lifecycle_approval_ids: list[UUID] | Unset = UNSET
    """Exact persisted successor-review receipts accepted inside this advance transaction."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        revision = self.revision

        deployment_id = str(self.deployment_id)

        status: str = self.status

        reasons = self.reasons

        check_queued = self.check_queued

        lifecycle_approval_ids: list[str] | Unset = UNSET
        if not isinstance(self.lifecycle_approval_ids, Unset):
            lifecycle_approval_ids = []
            for lifecycle_approval_ids_item_data in self.lifecycle_approval_ids:
                lifecycle_approval_ids_item = str(lifecycle_approval_ids_item_data)
                lifecycle_approval_ids.append(lifecycle_approval_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "revision": revision,
                "deployment_id": deployment_id,
                "status": status,
                "reasons": reasons,
                "check_queued": check_queued,
            }
        )
        if lifecycle_approval_ids is not UNSET:
            field_dict["lifecycle_approval_ids"] = lifecycle_approval_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_route_gate_decision_mode(d.pop("mode"))

        revision = d.pop("revision")

        deployment_id = UUID(d.pop("deployment_id"))

        status = check_route_gate_decision_status(d.pop("status"))

        reasons = cast(list[str], d.pop("reasons"))

        check_queued = d.pop("check_queued")

        _lifecycle_approval_ids = d.pop("lifecycle_approval_ids", UNSET)
        lifecycle_approval_ids: list[UUID] | Unset = UNSET
        if _lifecycle_approval_ids is not UNSET:
            lifecycle_approval_ids = []
            for lifecycle_approval_ids_item_data in _lifecycle_approval_ids:
                lifecycle_approval_ids_item = UUID(lifecycle_approval_ids_item_data)

                lifecycle_approval_ids.append(lifecycle_approval_ids_item)

        route_gate_decision = cls(
            mode=mode,
            revision=revision,
            deployment_id=deployment_id,
            status=status,
            reasons=reasons,
            check_queued=check_queued,
            lifecycle_approval_ids=lifecycle_approval_ids,
        )

        route_gate_decision.additional_properties = d
        return route_gate_decision

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
