from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_decision_mode import RouteHealthDecisionMode, check_route_health_decision_mode
from ..models.route_health_decision_on_regression import (
    RouteHealthDecisionOnRegression,
    check_route_health_decision_on_regression,
)
from ..models.route_health_decision_status import RouteHealthDecisionStatus, check_route_health_decision_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthDecision")


@_attrs_define
class RouteHealthDecision:
    """Metadata-only decision evaluated inside the canary traffic transaction; history_id correlates the exact saved
    evidence with the advance response and traffic audit.

    """

    mode: RouteHealthDecisionMode
    revision: int
    deployment_id: UUID
    stable_deployment_id: str
    status: RouteHealthDecisionStatus
    reason: str
    checked_at: datetime.datetime
    on_regression: RouteHealthDecisionOnRegression | Unset = UNSET
    """Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for
    confirmed route 5xx regressions during an enforced canary; report mode is observational."""
    history_id: UUID | Unset = UNSET
    """Retained saved decision UUID when selected routes were evaluated."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        revision = self.revision

        deployment_id = str(self.deployment_id)

        stable_deployment_id = self.stable_deployment_id

        status: str = self.status

        reason = self.reason

        checked_at = self.checked_at.isoformat()

        on_regression: str | Unset = UNSET
        if not isinstance(self.on_regression, Unset):
            on_regression = self.on_regression

        history_id: str | Unset = UNSET
        if not isinstance(self.history_id, Unset):
            history_id = str(self.history_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "revision": revision,
                "deployment_id": deployment_id,
                "stable_deployment_id": stable_deployment_id,
                "status": status,
                "reason": reason,
                "checked_at": checked_at,
            }
        )
        if on_regression is not UNSET:
            field_dict["on_regression"] = on_regression
        if history_id is not UNSET:
            field_dict["history_id"] = history_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_route_health_decision_mode(d.pop("mode"))

        revision = d.pop("revision")

        deployment_id = UUID(d.pop("deployment_id"))

        stable_deployment_id = d.pop("stable_deployment_id")

        status = check_route_health_decision_status(d.pop("status"))

        reason = d.pop("reason")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        _on_regression = d.pop("on_regression", UNSET)
        on_regression: RouteHealthDecisionOnRegression | Unset
        if isinstance(_on_regression, Unset):
            on_regression = UNSET
        else:
            on_regression = check_route_health_decision_on_regression(_on_regression)

        _history_id = d.pop("history_id", UNSET)
        history_id: UUID | Unset
        if isinstance(_history_id, Unset):
            history_id = UNSET
        else:
            history_id = UUID(_history_id)

        route_health_decision = cls(
            mode=mode,
            revision=revision,
            deployment_id=deployment_id,
            stable_deployment_id=stable_deployment_id,
            status=status,
            reason=reason,
            checked_at=checked_at,
            on_regression=on_regression,
            history_id=history_id,
        )

        route_health_decision.additional_properties = d
        return route_health_decision

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
