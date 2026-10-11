from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowCoordinationTimelineEvent")


@_attrs_define
class OperationWorkflowCoordinationTimelineEvent:
    """OperationWorkflowCoordinationTimelineEvent. Retained inbox coordination projection metadata."""

    id: str
    """OperationWorkflowCoordinationTimelineEvent.id. Stable identity within its subscription and notification."""
    at: datetime.datetime
    """OperationWorkflowCoordinationTimelineEvent.at. Retained server observation or action clock; never inferred
    application occurrence time."""
    kind: str
    """OperationWorkflowCoordinationTimelineEvent.kind. Notification, retained action, outcome observation or
    latest coordination snapshot."""
    summary: str
    """OperationWorkflowCoordinationTimelineEvent.summary. Explanation of the retained event and any evidence
    limitation."""
    subscription_id: UUID
    """OperationWorkflowCoordinationTimelineEvent.subscription_id. Source readiness subscription UUID."""
    subscription_name: str
    """OperationWorkflowCoordinationTimelineEvent.subscription_name. Current source subscription name."""
    notification_id: UUID
    """OperationWorkflowCoordinationTimelineEvent.notification_id. Source inbox notification UUID."""
    sequence: int
    """OperationWorkflowCoordinationTimelineEvent.sequence. Source inbox sequence."""
    reporting_operation_ids: list[UUID]
    """OperationWorkflowCoordinationTimelineEvent.reporting_operation_ids. Available operation evidence links;
    absence does not imply success or failure."""
    subscription_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.subscription_tenant_id. Operator-only original subscription
    tenant used to navigate the source inbox; omitted for broad subscriptions."""
    actor: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.actor. Retained coordination actor, when available."""
    owner: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.owner. Retained coordination owner label."""
    status: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.status. Status in the event kind context."""
    note: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.note. Retained human coordination note."""
    follow_up_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.follow_up_at. Associated follow-up or reminder due clock."""
    expected_outcome_code: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.expected_outcome_code. Application-declared expected business
    outcome."""
    reported_outcome_code: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.reported_outcome_code. Observed application-reported business
    outcome."""
    state_revision: int | Unset = UNSET
    """OperationWorkflowCoordinationTimelineEvent.state_revision. Observed workflow report revision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        at = self.at.isoformat()

        kind = self.kind

        summary = self.summary

        subscription_id = str(self.subscription_id)

        subscription_name = self.subscription_name

        notification_id = str(self.notification_id)

        sequence = self.sequence

        reporting_operation_ids = []
        for reporting_operation_ids_item_data in self.reporting_operation_ids:
            reporting_operation_ids_item = str(reporting_operation_ids_item_data)
            reporting_operation_ids.append(reporting_operation_ids_item)

        subscription_tenant_id: str | Unset = UNSET
        if not isinstance(self.subscription_tenant_id, Unset):
            subscription_tenant_id = str(self.subscription_tenant_id)

        actor = self.actor

        owner = self.owner

        status = self.status

        note = self.note

        follow_up_at: str | Unset = UNSET
        if not isinstance(self.follow_up_at, Unset):
            follow_up_at = self.follow_up_at.isoformat()

        expected_outcome_code = self.expected_outcome_code

        reported_outcome_code = self.reported_outcome_code

        state_revision = self.state_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "at": at,
                "kind": kind,
                "summary": summary,
                "subscription_id": subscription_id,
                "subscription_name": subscription_name,
                "notification_id": notification_id,
                "sequence": sequence,
                "reporting_operation_ids": reporting_operation_ids,
            }
        )
        if subscription_tenant_id is not UNSET:
            field_dict["subscription_tenant_id"] = subscription_tenant_id
        if actor is not UNSET:
            field_dict["actor"] = actor
        if owner is not UNSET:
            field_dict["owner"] = owner
        if status is not UNSET:
            field_dict["status"] = status
        if note is not UNSET:
            field_dict["note"] = note
        if follow_up_at is not UNSET:
            field_dict["follow_up_at"] = follow_up_at
        if expected_outcome_code is not UNSET:
            field_dict["expected_outcome_code"] = expected_outcome_code
        if reported_outcome_code is not UNSET:
            field_dict["reported_outcome_code"] = reported_outcome_code
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = d.pop("kind")

        summary = d.pop("summary")

        subscription_id = UUID(d.pop("subscription_id"))

        subscription_name = d.pop("subscription_name")

        notification_id = UUID(d.pop("notification_id"))

        sequence = d.pop("sequence")

        reporting_operation_ids = []
        _reporting_operation_ids = d.pop("reporting_operation_ids")
        for reporting_operation_ids_item_data in _reporting_operation_ids:
            reporting_operation_ids_item = UUID(reporting_operation_ids_item_data)

            reporting_operation_ids.append(reporting_operation_ids_item)

        _subscription_tenant_id = d.pop("subscription_tenant_id", UNSET)
        subscription_tenant_id: UUID | Unset
        if isinstance(_subscription_tenant_id, Unset):
            subscription_tenant_id = UNSET
        else:
            subscription_tenant_id = UUID(_subscription_tenant_id)

        actor = d.pop("actor", UNSET)

        owner = d.pop("owner", UNSET)

        status = d.pop("status", UNSET)

        note = d.pop("note", UNSET)

        _follow_up_at = d.pop("follow_up_at", UNSET)
        follow_up_at: datetime.datetime | Unset
        if isinstance(_follow_up_at, Unset):
            follow_up_at = UNSET
        else:
            follow_up_at = datetime.datetime.fromisoformat(_follow_up_at)

        expected_outcome_code = d.pop("expected_outcome_code", UNSET)

        reported_outcome_code = d.pop("reported_outcome_code", UNSET)

        state_revision = d.pop("state_revision", UNSET)

        operation_workflow_coordination_timeline_event = cls(
            id=id,
            at=at,
            kind=kind,
            summary=summary,
            subscription_id=subscription_id,
            subscription_name=subscription_name,
            notification_id=notification_id,
            sequence=sequence,
            reporting_operation_ids=reporting_operation_ids,
            subscription_tenant_id=subscription_tenant_id,
            actor=actor,
            owner=owner,
            status=status,
            note=note,
            follow_up_at=follow_up_at,
            expected_outcome_code=expected_outcome_code,
            reported_outcome_code=reported_outcome_code,
            state_revision=state_revision,
        )

        operation_workflow_coordination_timeline_event.additional_properties = d
        return operation_workflow_coordination_timeline_event

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
