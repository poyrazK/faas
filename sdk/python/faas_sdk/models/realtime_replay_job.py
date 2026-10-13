from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_replay_job_state import RealtimeReplayJobState, check_realtime_replay_job_state

if TYPE_CHECKING:
    from ..models.realtime_replay_job_outcome import RealtimeReplayJobOutcome


T = TypeVar("T", bound="RealtimeReplayJob")


@_attrs_define
class RealtimeReplayJob:
    """Realtime Replay Job."""

    id: UUID
    group_id: UUID
    state: RealtimeReplayJobState
    rate: int
    selected: int
    processed: int
    replayed: int
    skipped: int
    outcomes: list[RealtimeReplayJobOutcome]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    error_count: int
    last_error: str
    """Empty until a processing failure; processing_failed is a safe diagnostic without database details."""
    dry_run: bool
    eligible: int
    """Eligible checks in a dry-run report; zero for a real replay."""
    reviewed_job_id: None | UUID
    """Source dry-run report for a reviewed replay. History reference may outlive the report."""
    started_job_id: None | UUID
    """Single real replay started from this report. History reference may outlive the replay."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        group_id = str(self.group_id)

        state: str = self.state

        rate = self.rate

        selected = self.selected

        processed = self.processed

        replayed = self.replayed

        skipped = self.skipped

        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        error_count = self.error_count

        last_error = self.last_error

        dry_run = self.dry_run

        eligible = self.eligible

        reviewed_job_id: None | str
        if isinstance(self.reviewed_job_id, UUID):
            reviewed_job_id = str(self.reviewed_job_id)
        else:
            reviewed_job_id = self.reviewed_job_id

        started_job_id: None | str
        if isinstance(self.started_job_id, UUID):
            started_job_id = str(self.started_job_id)
        else:
            started_job_id = self.started_job_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "group_id": group_id,
                "state": state,
                "rate": rate,
                "selected": selected,
                "processed": processed,
                "replayed": replayed,
                "skipped": skipped,
                "outcomes": outcomes,
                "created_at": created_at,
                "updated_at": updated_at,
                "error_count": error_count,
                "last_error": last_error,
                "dry_run": dry_run,
                "eligible": eligible,
                "reviewed_job_id": reviewed_job_id,
                "started_job_id": started_job_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_replay_job_outcome import RealtimeReplayJobOutcome

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        group_id = UUID(d.pop("group_id"))

        state = check_realtime_replay_job_state(d.pop("state"))

        rate = d.pop("rate")

        selected = d.pop("selected")

        processed = d.pop("processed")

        replayed = d.pop("replayed")

        skipped = d.pop("skipped")

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = RealtimeReplayJobOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        error_count = d.pop("error_count")

        last_error = d.pop("last_error")

        dry_run = d.pop("dry_run")

        eligible = d.pop("eligible")

        def _parse_reviewed_job_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                reviewed_job_id_type_0 = UUID(data)

                return reviewed_job_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        reviewed_job_id = _parse_reviewed_job_id(d.pop("reviewed_job_id"))

        def _parse_started_job_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_job_id_type_0 = UUID(data)

                return started_job_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        started_job_id = _parse_started_job_id(d.pop("started_job_id"))

        realtime_replay_job = cls(
            id=id,
            group_id=group_id,
            state=state,
            rate=rate,
            selected=selected,
            processed=processed,
            replayed=replayed,
            skipped=skipped,
            outcomes=outcomes,
            created_at=created_at,
            updated_at=updated_at,
            error_count=error_count,
            last_error=last_error,
            dry_run=dry_run,
            eligible=eligible,
            reviewed_job_id=reviewed_job_id,
            started_job_id=started_job_id,
        )

        realtime_replay_job.additional_properties = d
        return realtime_replay_job

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
