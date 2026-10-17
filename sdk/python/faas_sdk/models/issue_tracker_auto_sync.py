from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_tracker_auto_sync_direction import (
    IssueTrackerAutoSyncDirection,
    check_issue_tracker_auto_sync_direction,
)
from ..models.issue_tracker_auto_sync_observed_state import (
    IssueTrackerAutoSyncObservedState,
    check_issue_tracker_auto_sync_observed_state,
)
from ..models.issue_tracker_auto_sync_outcome import IssueTrackerAutoSyncOutcome, check_issue_tracker_auto_sync_outcome
from ..models.issue_tracker_auto_sync_pending_state import (
    IssueTrackerAutoSyncPendingState,
    check_issue_tracker_auto_sync_pending_state,
)
from ..models.issue_tracker_auto_sync_provider_state import (
    IssueTrackerAutoSyncProviderState,
    check_issue_tracker_auto_sync_provider_state,
)
from ..models.issue_tracker_auto_sync_source_state import (
    IssueTrackerAutoSyncSourceState,
    check_issue_tracker_auto_sync_source_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueTrackerAutoSync")


@_attrs_define
class IssueTrackerAutoSync:
    """Opt-in tracker direction and latest durable worker outcome. Inbound resolutions require explicit fixing-deployment
    evidence.

    """

    link_id: UUID
    enabled: bool
    revision: int
    source_issue_id: UUID
    outcome: IssueTrackerAutoSyncOutcome
    direction: IssueTrackerAutoSyncDirection | Unset = UNSET
    """Outbound pushes Gregale lifecycle; inbound applies verified reopenings; both enables both behaviors."""
    observed_state: IssueTrackerAutoSyncObservedState | Unset = UNSET
    """Last provider lifecycle observed by the worker including changes held for review."""
    source_state: IssueTrackerAutoSyncSourceState | Unset = UNSET
    provider_state: IssueTrackerAutoSyncProviderState | Unset = UNSET
    pending_state: IssueTrackerAutoSyncPendingState | Unset = UNSET
    reason: str | Unset = UNSET
    last_checked_at: datetime.datetime | Unset = UNSET
    next_check_at: datetime.datetime | Unset = UNSET
    linear_open_state_id: UUID | Unset = UNSET
    """Selected open workflow destination for Linear policies."""
    linear_resolved_state_id: UUID | Unset = UNSET
    """Selected resolved workflow destination for Linear policies."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        link_id = str(self.link_id)

        enabled = self.enabled

        revision = self.revision

        source_issue_id = str(self.source_issue_id)

        outcome: str = self.outcome

        direction: str | Unset = UNSET
        if not isinstance(self.direction, Unset):
            direction = self.direction

        observed_state: str | Unset = UNSET
        if not isinstance(self.observed_state, Unset):
            observed_state = self.observed_state

        source_state: str | Unset = UNSET
        if not isinstance(self.source_state, Unset):
            source_state = self.source_state

        provider_state: str | Unset = UNSET
        if not isinstance(self.provider_state, Unset):
            provider_state = self.provider_state

        pending_state: str | Unset = UNSET
        if not isinstance(self.pending_state, Unset):
            pending_state = self.pending_state

        reason = self.reason

        last_checked_at: str | Unset = UNSET
        if not isinstance(self.last_checked_at, Unset):
            last_checked_at = self.last_checked_at.isoformat()

        next_check_at: str | Unset = UNSET
        if not isinstance(self.next_check_at, Unset):
            next_check_at = self.next_check_at.isoformat()

        linear_open_state_id: str | Unset = UNSET
        if not isinstance(self.linear_open_state_id, Unset):
            linear_open_state_id = str(self.linear_open_state_id)

        linear_resolved_state_id: str | Unset = UNSET
        if not isinstance(self.linear_resolved_state_id, Unset):
            linear_resolved_state_id = str(self.linear_resolved_state_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "link_id": link_id,
                "enabled": enabled,
                "revision": revision,
                "source_issue_id": source_issue_id,
                "outcome": outcome,
            }
        )
        if direction is not UNSET:
            field_dict["direction"] = direction
        if observed_state is not UNSET:
            field_dict["observed_state"] = observed_state
        if source_state is not UNSET:
            field_dict["source_state"] = source_state
        if provider_state is not UNSET:
            field_dict["provider_state"] = provider_state
        if pending_state is not UNSET:
            field_dict["pending_state"] = pending_state
        if reason is not UNSET:
            field_dict["reason"] = reason
        if last_checked_at is not UNSET:
            field_dict["last_checked_at"] = last_checked_at
        if next_check_at is not UNSET:
            field_dict["next_check_at"] = next_check_at
        if linear_open_state_id is not UNSET:
            field_dict["linear_open_state_id"] = linear_open_state_id
        if linear_resolved_state_id is not UNSET:
            field_dict["linear_resolved_state_id"] = linear_resolved_state_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        link_id = UUID(d.pop("link_id"))

        enabled = d.pop("enabled")

        revision = d.pop("revision")

        source_issue_id = UUID(d.pop("source_issue_id"))

        outcome = check_issue_tracker_auto_sync_outcome(d.pop("outcome"))

        _direction = d.pop("direction", UNSET)
        direction: IssueTrackerAutoSyncDirection | Unset
        if isinstance(_direction, Unset):
            direction = UNSET
        else:
            direction = check_issue_tracker_auto_sync_direction(_direction)

        _observed_state = d.pop("observed_state", UNSET)
        observed_state: IssueTrackerAutoSyncObservedState | Unset
        if isinstance(_observed_state, Unset):
            observed_state = UNSET
        else:
            observed_state = check_issue_tracker_auto_sync_observed_state(_observed_state)

        _source_state = d.pop("source_state", UNSET)
        source_state: IssueTrackerAutoSyncSourceState | Unset
        if isinstance(_source_state, Unset):
            source_state = UNSET
        else:
            source_state = check_issue_tracker_auto_sync_source_state(_source_state)

        _provider_state = d.pop("provider_state", UNSET)
        provider_state: IssueTrackerAutoSyncProviderState | Unset
        if isinstance(_provider_state, Unset):
            provider_state = UNSET
        else:
            provider_state = check_issue_tracker_auto_sync_provider_state(_provider_state)

        _pending_state = d.pop("pending_state", UNSET)
        pending_state: IssueTrackerAutoSyncPendingState | Unset
        if isinstance(_pending_state, Unset):
            pending_state = UNSET
        else:
            pending_state = check_issue_tracker_auto_sync_pending_state(_pending_state)

        reason = d.pop("reason", UNSET)

        _last_checked_at = d.pop("last_checked_at", UNSET)
        last_checked_at: datetime.datetime | Unset
        if isinstance(_last_checked_at, Unset):
            last_checked_at = UNSET
        else:
            last_checked_at = datetime.datetime.fromisoformat(_last_checked_at)

        _next_check_at = d.pop("next_check_at", UNSET)
        next_check_at: datetime.datetime | Unset
        if isinstance(_next_check_at, Unset):
            next_check_at = UNSET
        else:
            next_check_at = datetime.datetime.fromisoformat(_next_check_at)

        _linear_open_state_id = d.pop("linear_open_state_id", UNSET)
        linear_open_state_id: UUID | Unset
        if isinstance(_linear_open_state_id, Unset):
            linear_open_state_id = UNSET
        else:
            linear_open_state_id = UUID(_linear_open_state_id)

        _linear_resolved_state_id = d.pop("linear_resolved_state_id", UNSET)
        linear_resolved_state_id: UUID | Unset
        if isinstance(_linear_resolved_state_id, Unset):
            linear_resolved_state_id = UNSET
        else:
            linear_resolved_state_id = UUID(_linear_resolved_state_id)

        issue_tracker_auto_sync = cls(
            link_id=link_id,
            enabled=enabled,
            revision=revision,
            source_issue_id=source_issue_id,
            outcome=outcome,
            direction=direction,
            observed_state=observed_state,
            source_state=source_state,
            provider_state=provider_state,
            pending_state=pending_state,
            reason=reason,
            last_checked_at=last_checked_at,
            next_check_at=next_check_at,
            linear_open_state_id=linear_open_state_id,
            linear_resolved_state_id=linear_resolved_state_id,
        )

        issue_tracker_auto_sync.additional_properties = d
        return issue_tracker_auto_sync

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
