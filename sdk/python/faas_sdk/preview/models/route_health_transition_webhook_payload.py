from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_transition_webhook_payload_health_status import (
    RouteHealthTransitionWebhookPayloadHealthStatus,
    check_route_health_transition_webhook_payload_health_status,
)
from ..models.route_health_transition_webhook_payload_source import (
    RouteHealthTransitionWebhookPayloadSource,
    check_route_health_transition_webhook_payload_source,
)
from ..models.route_health_transition_webhook_payload_status import (
    RouteHealthTransitionWebhookPayloadStatus,
    check_route_health_transition_webhook_payload_status,
)
from ..models.route_health_transition_webhook_payload_version import (
    RouteHealthTransitionWebhookPayloadVersion,
    check_route_health_transition_webhook_payload_version,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthTransitionWebhookPayload")


@_attrs_define
class RouteHealthTransitionWebhookPayload:
    """Metadata-only health hold, committed resume, or committed automatic abort event. Produced by actual enforced canary
    advance evaluations. Unknown evidence never clears a confirmed regression; a fresh context never claims recovery.
    App-scoped recipients are captured with the decision. Fetch history_path with apps:read and completed MFA for route
    evidence; snapshots are subject to retention. Repeated worker retries do not emit duplicate holds. Automatic abort
    events describe a committed recovery from fresh confirmed 5xx evidence under an opt-in policy; receiving a webhook
    never authorizes a traffic mutation.

    """

    version: RouteHealthTransitionWebhookPayloadVersion
    app_id: UUID
    deployment_id: UUID
    stable_deployment_id: str
    """Empty when a unique stable deployment was unavailable."""
    decision_id: UUID
    status: RouteHealthTransitionWebhookPayloadStatus
    health_status: RouteHealthTransitionWebhookPayloadHealthStatus
    reason: str
    source: RouteHealthTransitionWebhookPayloadSource
    canary_step: int
    revision: int
    checked_at: datetime.datetime
    previous_traffic_percent: int
    requested_traffic_percent: int
    history_path: str
    """Authenticated single-entry API path for the exact saved decision."""
    blocked_decision_id: UUID | Unset = UNSET
    """The prior comparable hold decision for resumed or aborted events when a hold exists; may have been pruned."""
    observation_anchor: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        stable_deployment_id = self.stable_deployment_id

        decision_id = str(self.decision_id)

        status: str = self.status

        health_status: str = self.health_status

        reason = self.reason

        source: str = self.source

        canary_step = self.canary_step

        revision = self.revision

        checked_at = self.checked_at.isoformat()

        previous_traffic_percent = self.previous_traffic_percent

        requested_traffic_percent = self.requested_traffic_percent

        history_path = self.history_path

        blocked_decision_id: str | Unset = UNSET
        if not isinstance(self.blocked_decision_id, Unset):
            blocked_decision_id = str(self.blocked_decision_id)

        observation_anchor: str | Unset = UNSET
        if not isinstance(self.observation_anchor, Unset):
            observation_anchor = self.observation_anchor.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "stable_deployment_id": stable_deployment_id,
                "decision_id": decision_id,
                "status": status,
                "health_status": health_status,
                "reason": reason,
                "source": source,
                "canary_step": canary_step,
                "revision": revision,
                "checked_at": checked_at,
                "previous_traffic_percent": previous_traffic_percent,
                "requested_traffic_percent": requested_traffic_percent,
                "history_path": history_path,
            }
        )
        if blocked_decision_id is not UNSET:
            field_dict["blocked_decision_id"] = blocked_decision_id
        if observation_anchor is not UNSET:
            field_dict["observation_anchor"] = observation_anchor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_route_health_transition_webhook_payload_version(d.pop("version"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        stable_deployment_id = d.pop("stable_deployment_id")

        decision_id = UUID(d.pop("decision_id"))

        status = check_route_health_transition_webhook_payload_status(d.pop("status"))

        health_status = check_route_health_transition_webhook_payload_health_status(d.pop("health_status"))

        reason = d.pop("reason")

        source = check_route_health_transition_webhook_payload_source(d.pop("source"))

        canary_step = d.pop("canary_step")

        revision = d.pop("revision")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        previous_traffic_percent = d.pop("previous_traffic_percent")

        requested_traffic_percent = d.pop("requested_traffic_percent")

        history_path = d.pop("history_path")

        _blocked_decision_id = d.pop("blocked_decision_id", UNSET)
        blocked_decision_id: UUID | Unset
        if isinstance(_blocked_decision_id, Unset):
            blocked_decision_id = UNSET
        else:
            blocked_decision_id = UUID(_blocked_decision_id)

        _observation_anchor = d.pop("observation_anchor", UNSET)
        observation_anchor: datetime.datetime | Unset
        if isinstance(_observation_anchor, Unset):
            observation_anchor = UNSET
        else:
            observation_anchor = datetime.datetime.fromisoformat(_observation_anchor)

        route_health_transition_webhook_payload = cls(
            version=version,
            app_id=app_id,
            deployment_id=deployment_id,
            stable_deployment_id=stable_deployment_id,
            decision_id=decision_id,
            status=status,
            health_status=health_status,
            reason=reason,
            source=source,
            canary_step=canary_step,
            revision=revision,
            checked_at=checked_at,
            previous_traffic_percent=previous_traffic_percent,
            requested_traffic_percent=requested_traffic_percent,
            history_path=history_path,
            blocked_decision_id=blocked_decision_id,
            observation_anchor=observation_anchor,
        )

        route_health_transition_webhook_payload.additional_properties = d
        return route_health_transition_webhook_payload

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
