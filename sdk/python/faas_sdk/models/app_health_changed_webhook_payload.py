from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.app_health_changed_webhook_payload_change import (
    AppHealthChangedWebhookPayloadChange,
    check_app_health_changed_webhook_payload_change,
)
from ..models.app_health_changed_webhook_payload_phase import (
    AppHealthChangedWebhookPayloadPhase,
    check_app_health_changed_webhook_payload_phase,
)
from ..models.app_health_changed_webhook_payload_previous_status import (
    AppHealthChangedWebhookPayloadPreviousStatus,
    check_app_health_changed_webhook_payload_previous_status,
)
from ..models.app_health_changed_webhook_payload_scope import (
    AppHealthChangedWebhookPayloadScope,
    check_app_health_changed_webhook_payload_scope,
)
from ..models.app_health_changed_webhook_payload_status import (
    AppHealthChangedWebhookPayloadStatus,
    check_app_health_changed_webhook_payload_status,
)
from ..models.app_health_changed_webhook_payload_version import (
    AppHealthChangedWebhookPayloadVersion,
    check_app_health_changed_webhook_payload_version,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppHealthChangedWebhookPayload")


@_attrs_define
class AppHealthChangedWebhookPayload:
    """Versioned app.health.changed data. Explicit app-owned subscriptions
    receive sampled status changes after a silent baseline. At most one
    event per app every five minutes; pending changes combine to the latest
    observed status, and returning to the last announced status cancels them.
    Evidence gaps reset the comparison without inferring recovery. Unknown
    is a confidence change, never a confirmed outage or recovery. The
    transition points to retained history; evaluated_at describes the fresh
    assessment used to queue this event. Delivery remains at least once.

    """

    version: AppHealthChangedWebhookPayloadVersion
    app_id: str
    scope: AppHealthChangedWebhookPayloadScope
    transition_id: UUID
    transition_observed_at: datetime.datetime
    previous_status: AppHealthChangedWebhookPayloadPreviousStatus
    """Last announced or silently established comparison status."""
    status: AppHealthChangedWebhookPayloadStatus
    change: AppHealthChangedWebhookPayloadChange
    phase: AppHealthChangedWebhookPayloadPhase
    evaluated_at: datetime.datetime
    queued_at: datetime.datetime
    coalesced: bool
    """Changes were combined during the notification cooldown."""
    cooldown_seconds: int
    """Minimum spacing between app health notification intents."""
    serving_deployment_ids: list[str]
    history_path: str
    """Authenticated cursor-paged history; locate transition_id while retained."""
    latest_deployment_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        app_id = self.app_id

        scope: str = self.scope

        transition_id = str(self.transition_id)

        transition_observed_at = self.transition_observed_at.isoformat()

        previous_status: str = self.previous_status

        status: str = self.status

        change: str = self.change

        phase: str = self.phase

        evaluated_at = self.evaluated_at.isoformat()

        queued_at = self.queued_at.isoformat()

        coalesced = self.coalesced

        cooldown_seconds = self.cooldown_seconds

        serving_deployment_ids = self.serving_deployment_ids

        history_path = self.history_path

        latest_deployment_id = self.latest_deployment_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "scope": scope,
                "transition_id": transition_id,
                "transition_observed_at": transition_observed_at,
                "previous_status": previous_status,
                "status": status,
                "change": change,
                "phase": phase,
                "evaluated_at": evaluated_at,
                "queued_at": queued_at,
                "coalesced": coalesced,
                "cooldown_seconds": cooldown_seconds,
                "serving_deployment_ids": serving_deployment_ids,
                "history_path": history_path,
            }
        )
        if latest_deployment_id is not UNSET:
            field_dict["latest_deployment_id"] = latest_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_app_health_changed_webhook_payload_version(d.pop("version"))

        app_id = d.pop("app_id")

        scope = check_app_health_changed_webhook_payload_scope(d.pop("scope"))

        transition_id = UUID(d.pop("transition_id"))

        transition_observed_at = datetime.datetime.fromisoformat(d.pop("transition_observed_at"))

        previous_status = check_app_health_changed_webhook_payload_previous_status(d.pop("previous_status"))

        status = check_app_health_changed_webhook_payload_status(d.pop("status"))

        change = check_app_health_changed_webhook_payload_change(d.pop("change"))

        phase = check_app_health_changed_webhook_payload_phase(d.pop("phase"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        queued_at = datetime.datetime.fromisoformat(d.pop("queued_at"))

        coalesced = d.pop("coalesced")

        cooldown_seconds = d.pop("cooldown_seconds")

        serving_deployment_ids = cast(list[str], d.pop("serving_deployment_ids"))

        history_path = d.pop("history_path")

        latest_deployment_id = d.pop("latest_deployment_id", UNSET)

        app_health_changed_webhook_payload = cls(
            version=version,
            app_id=app_id,
            scope=scope,
            transition_id=transition_id,
            transition_observed_at=transition_observed_at,
            previous_status=previous_status,
            status=status,
            change=change,
            phase=phase,
            evaluated_at=evaluated_at,
            queued_at=queued_at,
            coalesced=coalesced,
            cooldown_seconds=cooldown_seconds,
            serving_deployment_ids=serving_deployment_ids,
            history_path=history_path,
            latest_deployment_id=latest_deployment_id,
        )

        return app_health_changed_webhook_payload
