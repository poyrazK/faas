from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookEvidenceWatchRequest")


@_attrs_define
class OperationWorkflowPlaybookEvidenceWatchRequest:
    """OperationWorkflowPlaybookEvidenceWatchRequest. Opt-in durable checklist evidence monitoring; advisory observations
    preserve human reviews and application business authority.

    """

    subscription_id: UUID
    """OperationWorkflowPlaybookEvidenceWatchRequest.subscription_id. Existing inbox destination in the same
    application, environment, and caller access boundary."""
    progress_revision: int
    """OperationWorkflowPlaybookEvidenceWatchRequest.progress_revision. Expected exact human checklist revision;
    stale observations conflict."""
    revision: int
    """OperationWorkflowPlaybookEvidenceWatchRequest.revision. Watch configuration revision. Zero creates; polling
    leaves it unchanged."""
    enabled: bool
    """OperationWorkflowPlaybookEvidenceWatchRequest.enabled. Explicit monitoring preference; enabling establishes
    a silent baseline."""
    owner: str
    """OperationWorkflowPlaybookEvidenceWatchRequest.owner. Responsible team label assigned to new inbox
    deliveries; no external destination."""
    watch_id: UUID | Unset = UNSET
    """OperationWorkflowPlaybookEvidenceWatchRequest.watch_id. Required current watch UUID when changing
    configuration; omit for a new watch. Prevents stale edits after a stopped slot is reclaimed."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        progress_revision = self.progress_revision

        revision = self.revision

        enabled = self.enabled

        owner = self.owner

        watch_id: str | Unset = UNSET
        if not isinstance(self.watch_id, Unset):
            watch_id = str(self.watch_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "progress_revision": progress_revision,
                "revision": revision,
                "enabled": enabled,
                "owner": owner,
            }
        )
        if watch_id is not UNSET:
            field_dict["watch_id"] = watch_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        progress_revision = d.pop("progress_revision")

        revision = d.pop("revision")

        enabled = d.pop("enabled")

        owner = d.pop("owner")

        _watch_id = d.pop("watch_id", UNSET)
        watch_id: UUID | Unset
        if isinstance(_watch_id, Unset):
            watch_id = UNSET
        else:
            watch_id = UUID(_watch_id)

        operation_workflow_playbook_evidence_watch_request = cls(
            subscription_id=subscription_id,
            progress_revision=progress_revision,
            revision=revision,
            enabled=enabled,
            owner=owner,
            watch_id=watch_id,
        )

        return operation_workflow_playbook_evidence_watch_request
