from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.workflow_recovery_polling_webhook_payload_code import (
    WorkflowRecoveryPollingWebhookPayloadCode,
    check_workflow_recovery_polling_webhook_payload_code,
)
from ..models.workflow_recovery_polling_webhook_payload_scope import (
    WorkflowRecoveryPollingWebhookPayloadScope,
    check_workflow_recovery_polling_webhook_payload_scope,
)
from ..models.workflow_recovery_polling_webhook_payload_state import (
    WorkflowRecoveryPollingWebhookPayloadState,
    check_workflow_recovery_polling_webhook_payload_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryPollingWebhookPayload")


@_attrs_define
class WorkflowRecoveryPollingWebhookPayload:
    """Historical automation.recovery_polling_status event. Three consecutive deterministic polling failures trigger
    failed; unchanged code and policy version stay quiet. Recovered proves a successful poll, independently of workflow
    outcome. Recipients and payload are frozen at capture; use event_id for deduplication.

    """

    event_id: UUID
    app_id: UUID
    app: str
    scope: WorkflowRecoveryPollingWebhookPayloadScope
    policy_version: int
    sequence: int
    state: WorkflowRecoveryPollingWebhookPayloadState
    code: WorkflowRecoveryPollingWebhookPayloadCode
    attempts: int
    observed_at: datetime.datetime
    inspection_url: str
    monitoring_only: bool
    """Always true; successful polling does not prove successful workflow execution."""
    review_id: UUID | Unset = UNSET
    retry_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        app_id = str(self.app_id)

        app = self.app

        scope: str = self.scope

        policy_version = self.policy_version

        sequence = self.sequence

        state: str = self.state

        code: str = self.code

        attempts = self.attempts

        observed_at = self.observed_at.isoformat()

        inspection_url = self.inspection_url

        monitoring_only = self.monitoring_only

        review_id: str | Unset = UNSET
        if not isinstance(self.review_id, Unset):
            review_id = str(self.review_id)

        retry_at: str | Unset = UNSET
        if not isinstance(self.retry_at, Unset):
            retry_at = self.retry_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "app_id": app_id,
                "app": app,
                "scope": scope,
                "policy_version": policy_version,
                "sequence": sequence,
                "state": state,
                "code": code,
                "attempts": attempts,
                "observed_at": observed_at,
                "inspection_url": inspection_url,
                "monitoring_only": monitoring_only,
            }
        )
        if review_id is not UNSET:
            field_dict["review_id"] = review_id
        if retry_at is not UNSET:
            field_dict["retry_at"] = retry_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        app_id = UUID(d.pop("app_id"))

        app = d.pop("app")

        scope = check_workflow_recovery_polling_webhook_payload_scope(d.pop("scope"))

        policy_version = d.pop("policy_version")

        sequence = d.pop("sequence")

        state = check_workflow_recovery_polling_webhook_payload_state(d.pop("state"))

        code = check_workflow_recovery_polling_webhook_payload_code(d.pop("code"))

        attempts = d.pop("attempts")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        inspection_url = d.pop("inspection_url")

        monitoring_only = d.pop("monitoring_only")

        _review_id = d.pop("review_id", UNSET)
        review_id: UUID | Unset
        if isinstance(_review_id, Unset):
            review_id = UNSET
        else:
            review_id = UUID(_review_id)

        _retry_at = d.pop("retry_at", UNSET)
        retry_at: datetime.datetime | Unset
        if isinstance(_retry_at, Unset):
            retry_at = UNSET
        else:
            retry_at = datetime.datetime.fromisoformat(_retry_at)

        workflow_recovery_polling_webhook_payload = cls(
            event_id=event_id,
            app_id=app_id,
            app=app,
            scope=scope,
            policy_version=policy_version,
            sequence=sequence,
            state=state,
            code=code,
            attempts=attempts,
            observed_at=observed_at,
            inspection_url=inspection_url,
            monitoring_only=monitoring_only,
            review_id=review_id,
            retry_at=retry_at,
        )

        return workflow_recovery_polling_webhook_payload
