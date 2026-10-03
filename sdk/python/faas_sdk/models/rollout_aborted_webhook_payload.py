from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rollout_aborted_webhook_payload_rollout_state import (
    RolloutAbortedWebhookPayloadRolloutState,
    check_rollout_aborted_webhook_payload_rollout_state,
)

T = TypeVar("T", bound="RolloutAbortedWebhookPayload")


@_attrs_define
class RolloutAbortedWebhookPayload:
    """A live rollout was aborted. Pre-live build failures emit deployment.failed instead."""

    app_id: str
    deployment_id: str
    rollout_state: RolloutAbortedWebhookPayloadRolloutState
    traffic_percent: int
    reason: str
    aborted_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        rollout_state: str = self.rollout_state

        traffic_percent = self.traffic_percent

        reason = self.reason

        aborted_at = self.aborted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "rollout_state": rollout_state,
                "traffic_percent": traffic_percent,
                "reason": reason,
                "aborted_at": aborted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        rollout_state = check_rollout_aborted_webhook_payload_rollout_state(d.pop("rollout_state"))

        traffic_percent = d.pop("traffic_percent")

        reason = d.pop("reason")

        aborted_at = datetime.datetime.fromisoformat(d.pop("aborted_at"))

        rollout_aborted_webhook_payload = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            rollout_state=rollout_state,
            traffic_percent=traffic_percent,
            reason=reason,
            aborted_at=aborted_at,
        )

        rollout_aborted_webhook_payload.additional_properties = d
        return rollout_aborted_webhook_payload

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
