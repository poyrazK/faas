from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_event_replay_preview_match_original_recipient import (
    WorkflowEventReplayPreviewMatchOriginalRecipient,
    check_workflow_event_replay_preview_match_original_recipient,
)
from ..models.workflow_event_replay_preview_match_routing_state import (
    WorkflowEventReplayPreviewMatchRoutingState,
    check_workflow_event_replay_preview_match_routing_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowEventReplayPreviewMatch")


@_attrs_define
class WorkflowEventReplayPreviewMatch:
    """A retained event whose captured workflow trigger matched, with routing and durable admission state."""

    event_id: str
    event_source: str
    event_type: str
    accepted_at: datetime.datetime
    original_recipient: WorkflowEventReplayPreviewMatchOriginalRecipient
    """The immutable event snapshot contains this workflow recipient."""
    routing_state: WorkflowEventReplayPreviewMatchRoutingState
    """Latest retained routing checkpoint for this captured workflow recipient."""
    filter_matched: bool
    """The captured workflow trigger filter matches the retained envelope."""
    admission_recorded: bool
    """A durable workflow admission receipt exists. This is the event/recipient deduplication key even if its run
    was later pruned."""
    receipt_url: str
    schema_version: str | Unset = UNSET
    workflow_run_id: UUID | Unset = UNSET
    """Run ID while the linked workflow run remains retained."""
    workflow_run_status: str | Unset = UNSET
    """Current retained workflow run state when the run still exists."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        event_type = self.event_type

        accepted_at = self.accepted_at.isoformat()

        original_recipient: str = self.original_recipient

        routing_state: str = self.routing_state

        filter_matched = self.filter_matched

        admission_recorded = self.admission_recorded

        receipt_url = self.receipt_url

        schema_version = self.schema_version

        workflow_run_id: str | Unset = UNSET
        if not isinstance(self.workflow_run_id, Unset):
            workflow_run_id = str(self.workflow_run_id)

        workflow_run_status = self.workflow_run_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "event_type": event_type,
                "accepted_at": accepted_at,
                "original_recipient": original_recipient,
                "routing_state": routing_state,
                "filter_matched": filter_matched,
                "admission_recorded": admission_recorded,
                "receipt_url": receipt_url,
            }
        )
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id
        if workflow_run_status is not UNSET:
            field_dict["workflow_run_status"] = workflow_run_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        event_type = d.pop("event_type")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        original_recipient = check_workflow_event_replay_preview_match_original_recipient(d.pop("original_recipient"))

        routing_state = check_workflow_event_replay_preview_match_routing_state(d.pop("routing_state"))

        filter_matched = d.pop("filter_matched")

        admission_recorded = d.pop("admission_recorded")

        receipt_url = d.pop("receipt_url")

        schema_version = d.pop("schema_version", UNSET)

        _workflow_run_id = d.pop("workflow_run_id", UNSET)
        workflow_run_id: UUID | Unset
        if isinstance(_workflow_run_id, Unset):
            workflow_run_id = UNSET
        else:
            workflow_run_id = UUID(_workflow_run_id)

        workflow_run_status = d.pop("workflow_run_status", UNSET)

        workflow_event_replay_preview_match = cls(
            event_id=event_id,
            event_source=event_source,
            event_type=event_type,
            accepted_at=accepted_at,
            original_recipient=original_recipient,
            routing_state=routing_state,
            filter_matched=filter_matched,
            admission_recorded=admission_recorded,
            receipt_url=receipt_url,
            schema_version=schema_version,
            workflow_run_id=workflow_run_id,
            workflow_run_status=workflow_run_status,
        )

        workflow_event_replay_preview_match.additional_properties = d
        return workflow_event_replay_preview_match

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
