from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_replay_backfill_job_response_duplicate_policy import (
    EventReplayBackfillJobResponseDuplicatePolicy,
    check_event_replay_backfill_job_response_duplicate_policy,
)
from ..models.event_replay_backfill_job_response_state import (
    EventReplayBackfillJobResponseState,
    check_event_replay_backfill_job_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_replay_backfill_progress import EventReplayBackfillProgress


T = TypeVar("T", bound="EventReplayBackfillJobResponse")


@_attrs_define
class EventReplayBackfillJobResponse:
    """Progress and immutable selection details for one durable event backfill."""

    id: UUID
    app_slug: str
    subscription_id: UUID
    subscription_revision: str
    """Fingerprint of the subscription declaration snapshotted when the job was created."""
    from_: datetime.datetime
    until: datetime.datetime
    cutoff_at: datetime.datetime
    """Fixed exclusive acceptance cutoff."""
    history_complete: bool
    """Always false; retained receipts are not an archive."""
    duplicate_policy: EventReplayBackfillJobResponseDuplicatePolicy
    state: EventReplayBackfillJobResponseState
    scan_complete: bool
    """Whether the retained range has been fully examined."""
    progress: EventReplayBackfillProgress
    """Durable scan outcomes and current routing state for a backfill job."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    earliest_retained_at: datetime.datetime | Unset = UNSET
    """Account-wide earliest event surviving when the job was created; does not establish complete coverage."""
    completed_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_slug = self.app_slug

        subscription_id = str(self.subscription_id)

        subscription_revision = self.subscription_revision

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        cutoff_at = self.cutoff_at.isoformat()

        history_complete = self.history_complete

        duplicate_policy: str = self.duplicate_policy

        state: str = self.state

        scan_complete = self.scan_complete

        progress = self.progress.to_dict()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        earliest_retained_at: str | Unset = UNSET
        if not isinstance(self.earliest_retained_at, Unset):
            earliest_retained_at = self.earliest_retained_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "app_slug": app_slug,
                "subscription_id": subscription_id,
                "subscription_revision": subscription_revision,
                "from": from_,
                "until": until,
                "cutoff_at": cutoff_at,
                "history_complete": history_complete,
                "duplicate_policy": duplicate_policy,
                "state": state,
                "scan_complete": scan_complete,
                "progress": progress,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if earliest_retained_at is not UNSET:
            field_dict["earliest_retained_at"] = earliest_retained_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_replay_backfill_progress import EventReplayBackfillProgress

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_slug = d.pop("app_slug")

        subscription_id = UUID(d.pop("subscription_id"))

        subscription_revision = d.pop("subscription_revision")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        cutoff_at = datetime.datetime.fromisoformat(d.pop("cutoff_at"))

        history_complete = d.pop("history_complete")

        duplicate_policy = check_event_replay_backfill_job_response_duplicate_policy(d.pop("duplicate_policy"))

        state = check_event_replay_backfill_job_response_state(d.pop("state"))

        scan_complete = d.pop("scan_complete")

        progress = EventReplayBackfillProgress.from_dict(d.pop("progress"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _earliest_retained_at = d.pop("earliest_retained_at", UNSET)
        earliest_retained_at: datetime.datetime | Unset
        if isinstance(_earliest_retained_at, Unset):
            earliest_retained_at = UNSET
        else:
            earliest_retained_at = datetime.datetime.fromisoformat(_earliest_retained_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        event_replay_backfill_job_response = cls(
            id=id,
            app_slug=app_slug,
            subscription_id=subscription_id,
            subscription_revision=subscription_revision,
            from_=from_,
            until=until,
            cutoff_at=cutoff_at,
            history_complete=history_complete,
            duplicate_policy=duplicate_policy,
            state=state,
            scan_complete=scan_complete,
            progress=progress,
            created_at=created_at,
            updated_at=updated_at,
            earliest_retained_at=earliest_retained_at,
            completed_at=completed_at,
        )

        return event_replay_backfill_job_response
