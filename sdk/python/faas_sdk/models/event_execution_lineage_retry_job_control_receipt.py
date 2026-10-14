from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_execution_lineage_retry_job_control_receipt_action import (
    EventExecutionLineageRetryJobControlReceiptAction,
    check_event_execution_lineage_retry_job_control_receipt_action,
)
from ..models.event_execution_lineage_retry_job_control_receipt_actor_kind import (
    EventExecutionLineageRetryJobControlReceiptActorKind,
    check_event_execution_lineage_retry_job_control_receipt_actor_kind,
)
from ..models.event_execution_lineage_retry_job_control_receipt_credential_type import (
    EventExecutionLineageRetryJobControlReceiptCredentialType,
    check_event_execution_lineage_retry_job_control_receipt_credential_type,
)
from ..models.event_execution_lineage_retry_job_control_receipt_result_status import (
    EventExecutionLineageRetryJobControlReceiptResultStatus,
    check_event_execution_lineage_retry_job_control_receipt_result_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventExecutionLineageRetryJobControlReceipt")


@_attrs_define
class EventExecutionLineageRetryJobControlReceipt:
    """Immutable original successful control outcome with command identity, progress, and trusted actor attribution."""

    command_id: UUID
    sequence: int
    job_request_id: UUID
    subscription_id: UUID
    app_id: UUID
    action: EventExecutionLineageRetryJobControlReceiptAction
    expected_created_at: datetime.datetime
    result_status: EventExecutionLineageRetryJobControlReceiptResultStatus
    """Immutable job state observed by the original command."""
    processed: int
    selected: int
    job_updated_at: datetime.datetime
    recorded_at: datetime.datetime
    retain_until: datetime.datetime
    actor_kind: EventExecutionLineageRetryJobControlReceiptActorKind | Unset = UNSET
    """Server-resolved original command actor; absent on legacy receipts."""
    actor_id: str | Unset = UNSET
    """Account UUID for a session, credential UUID for a bearer, or store for an internal call. Never a token value
    or session ID."""
    credential_type: EventExecutionLineageRetryJobControlReceiptCredentialType | Unset = UNSET
    """Original server-resolved authentication type; absent on legacy receipts."""

    def to_dict(self) -> dict[str, Any]:
        command_id = str(self.command_id)

        sequence = self.sequence

        job_request_id = str(self.job_request_id)

        subscription_id = str(self.subscription_id)

        app_id = str(self.app_id)

        action: str = self.action

        expected_created_at = self.expected_created_at.isoformat()

        result_status: str = self.result_status

        processed = self.processed

        selected = self.selected

        job_updated_at = self.job_updated_at.isoformat()

        recorded_at = self.recorded_at.isoformat()

        retain_until = self.retain_until.isoformat()

        actor_kind: str | Unset = UNSET
        if not isinstance(self.actor_kind, Unset):
            actor_kind = self.actor_kind

        actor_id = self.actor_id

        credential_type: str | Unset = UNSET
        if not isinstance(self.credential_type, Unset):
            credential_type = self.credential_type

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command_id": command_id,
                "sequence": sequence,
                "job_request_id": job_request_id,
                "subscription_id": subscription_id,
                "app_id": app_id,
                "action": action,
                "expected_created_at": expected_created_at,
                "result_status": result_status,
                "processed": processed,
                "selected": selected,
                "job_updated_at": job_updated_at,
                "recorded_at": recorded_at,
                "retain_until": retain_until,
            }
        )
        if actor_kind is not UNSET:
            field_dict["actor_kind"] = actor_kind
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id
        if credential_type is not UNSET:
            field_dict["credential_type"] = credential_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command_id = UUID(d.pop("command_id"))

        sequence = d.pop("sequence")

        job_request_id = UUID(d.pop("job_request_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        app_id = UUID(d.pop("app_id"))

        action = check_event_execution_lineage_retry_job_control_receipt_action(d.pop("action"))

        expected_created_at = datetime.datetime.fromisoformat(d.pop("expected_created_at"))

        result_status = check_event_execution_lineage_retry_job_control_receipt_result_status(d.pop("result_status"))

        processed = d.pop("processed")

        selected = d.pop("selected")

        job_updated_at = datetime.datetime.fromisoformat(d.pop("job_updated_at"))

        recorded_at = datetime.datetime.fromisoformat(d.pop("recorded_at"))

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        _actor_kind = d.pop("actor_kind", UNSET)
        actor_kind: EventExecutionLineageRetryJobControlReceiptActorKind | Unset
        if isinstance(_actor_kind, Unset):
            actor_kind = UNSET
        else:
            actor_kind = check_event_execution_lineage_retry_job_control_receipt_actor_kind(_actor_kind)

        actor_id = d.pop("actor_id", UNSET)

        _credential_type = d.pop("credential_type", UNSET)
        credential_type: EventExecutionLineageRetryJobControlReceiptCredentialType | Unset
        if isinstance(_credential_type, Unset):
            credential_type = UNSET
        else:
            credential_type = check_event_execution_lineage_retry_job_control_receipt_credential_type(_credential_type)

        event_execution_lineage_retry_job_control_receipt = cls(
            command_id=command_id,
            sequence=sequence,
            job_request_id=job_request_id,
            subscription_id=subscription_id,
            app_id=app_id,
            action=action,
            expected_created_at=expected_created_at,
            result_status=result_status,
            processed=processed,
            selected=selected,
            job_updated_at=job_updated_at,
            recorded_at=recorded_at,
            retain_until=retain_until,
            actor_kind=actor_kind,
            actor_id=actor_id,
            credential_type=credential_type,
        )

        return event_execution_lineage_retry_job_control_receipt
