from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_gate_decision_mode import RouteGateDecisionMode, check_route_gate_decision_mode
from ..models.route_gate_decision_reasons_item import (
    RouteGateDecisionReasonsItem,
    check_route_gate_decision_reasons_item,
)
from ..models.route_gate_decision_status import RouteGateDecisionStatus, check_route_gate_decision_status

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
    reasons: list[RouteGateDecisionReasonsItem]
    check_queued: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        revision = self.revision

        deployment_id = str(self.deployment_id)

        status: str = self.status

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        check_queued = self.check_queued

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_route_gate_decision_mode(d.pop("mode"))

        revision = d.pop("revision")

        deployment_id = UUID(d.pop("deployment_id"))

        status = check_route_gate_decision_status(d.pop("status"))

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_route_gate_decision_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        check_queued = d.pop("check_queued")

        route_gate_decision = cls(
            mode=mode,
            revision=revision,
            deployment_id=deployment_id,
            status=status,
            reasons=reasons,
            check_queued=check_queued,
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
