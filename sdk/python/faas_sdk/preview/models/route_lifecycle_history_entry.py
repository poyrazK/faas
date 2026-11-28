from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_lifecycle_history_entry_outcome import (
    RouteLifecycleHistoryEntryOutcome,
    check_route_lifecycle_history_entry_outcome,
)

if TYPE_CHECKING:
    from ..models.route_gate_decision import RouteGateDecision
    from ..models.route_lifecycle_history_approval import RouteLifecycleHistoryApproval
    from ..models.route_lifecycle_history_capture import RouteLifecycleHistoryCapture


T = TypeVar("T", bound="RouteLifecycleHistoryEntry")


@_attrs_define
class RouteLifecycleHistoryEntry:
    """Retained production review outcome and bounded historical evidence."""

    id: str
    app_id: str
    deployment_id: str
    reviewed_at: datetime.datetime
    scope: str
    outcome: RouteLifecycleHistoryEntryOutcome
    recovery: bool
    decision: RouteGateDecision
    """Metadata-only decision from the same transaction as a canary traffic advance. Findings remain in the route
    result API."""
    evidence_available: bool
    truncated: bool
    captures: list[RouteLifecycleHistoryCapture]
    graph_ids: list[str]
    approvals: list[RouteLifecycleHistoryApproval]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        deployment_id = self.deployment_id

        reviewed_at = self.reviewed_at.isoformat()

        scope = self.scope

        outcome: str = self.outcome

        recovery = self.recovery

        decision = self.decision.to_dict()

        evidence_available = self.evidence_available

        truncated = self.truncated

        captures = []
        for captures_item_data in self.captures:
            captures_item = captures_item_data.to_dict()
            captures.append(captures_item)

        graph_ids = self.graph_ids

        approvals = []
        for approvals_item_data in self.approvals:
            approvals_item = approvals_item_data.to_dict()
            approvals.append(approvals_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "reviewed_at": reviewed_at,
                "scope": scope,
                "outcome": outcome,
                "recovery": recovery,
                "decision": decision,
                "evidence_available": evidence_available,
                "truncated": truncated,
                "captures": captures,
                "graph_ids": graph_ids,
                "approvals": approvals,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_gate_decision import RouteGateDecision
        from ..models.route_lifecycle_history_approval import RouteLifecycleHistoryApproval
        from ..models.route_lifecycle_history_capture import RouteLifecycleHistoryCapture

        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        reviewed_at = datetime.datetime.fromisoformat(d.pop("reviewed_at"))

        scope = d.pop("scope")

        outcome = check_route_lifecycle_history_entry_outcome(d.pop("outcome"))

        recovery = d.pop("recovery")

        decision = RouteGateDecision.from_dict(d.pop("decision"))

        evidence_available = d.pop("evidence_available")

        truncated = d.pop("truncated")

        captures = []
        _captures = d.pop("captures")
        for captures_item_data in _captures:
            captures_item = RouteLifecycleHistoryCapture.from_dict(captures_item_data)

            captures.append(captures_item)

        graph_ids = cast(list[str], d.pop("graph_ids"))

        approvals = []
        _approvals = d.pop("approvals")
        for approvals_item_data in _approvals:
            approvals_item = RouteLifecycleHistoryApproval.from_dict(approvals_item_data)

            approvals.append(approvals_item)

        route_lifecycle_history_entry = cls(
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            reviewed_at=reviewed_at,
            scope=scope,
            outcome=outcome,
            recovery=recovery,
            decision=decision,
            evidence_available=evidence_available,
            truncated=truncated,
            captures=captures,
            graph_ids=graph_ids,
            approvals=approvals,
        )

        route_lifecycle_history_entry.additional_properties = d
        return route_lifecycle_history_entry

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
