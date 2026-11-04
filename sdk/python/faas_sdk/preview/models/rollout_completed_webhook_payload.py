from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rollout_completed_webhook_payload_rollout_state import (
    RolloutCompletedWebhookPayloadRolloutState,
    check_rollout_completed_webhook_payload_rollout_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.rollout_decision import RolloutDecision


T = TypeVar("T", bound="RolloutCompletedWebhookPayload")


@_attrs_define
class RolloutCompletedWebhookPayload:
    """The configured rollout completed. Explicit traffic splits may complete below 100%; inspect traffic_percent."""

    app_id: str
    deployment_id: str
    rollout_state: RolloutCompletedWebhookPayloadRolloutState
    traffic_percent: int
    completed_at: datetime.datetime
    decision: RolloutDecision | Unset = UNSET
    """Evidence behind one decision about a rollout: why a canary stage advanced, is held, or was aborted.
    Aggregates only; no request payloads, routes, URLs or customer text."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        rollout_state: str = self.rollout_state

        traffic_percent = self.traffic_percent

        completed_at = self.completed_at.isoformat()

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "rollout_state": rollout_state,
                "traffic_percent": traffic_percent,
                "completed_at": completed_at,
            }
        )
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.rollout_decision import RolloutDecision

        d = dict(src_dict)
        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        rollout_state = check_rollout_completed_webhook_payload_rollout_state(d.pop("rollout_state"))

        traffic_percent = d.pop("traffic_percent")

        completed_at = datetime.datetime.fromisoformat(d.pop("completed_at"))

        _decision = d.pop("decision", UNSET)
        decision: RolloutDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = RolloutDecision.from_dict(_decision)

        rollout_completed_webhook_payload = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            rollout_state=rollout_state,
            traffic_percent=traffic_percent,
            completed_at=completed_at,
            decision=decision,
        )

        rollout_completed_webhook_payload.additional_properties = d
        return rollout_completed_webhook_payload

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
