from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_schema_rollout_validation import EventSchemaRolloutValidation


T = TypeVar("T", bound="EventSchemaRolloutRetained")


@_attrs_define
class EventSchemaRolloutRetained:
    """Retained sample coverage and compatibility observations for the candidate event schema."""

    requested: bool
    scanned_count: int
    """Account receipts scanned, including unrelated sources and types."""
    examined_count: int
    valid_count: int
    invalid_count: int
    unreadable_count: int
    """Oversized, malformed, or invalid retained envelopes excluded from validation."""
    truncated: bool
    """Account scan, matching payload limit, or byte budget prevented checking all candidates."""
    history_complete: bool
    """Retention and bounded sampling never certify complete history."""
    results: list[EventSchemaRolloutValidation]
    from_: datetime.datetime | Unset = UNSET
    until: datetime.datetime | Unset = UNSET
    cutoff_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        requested = self.requested

        scanned_count = self.scanned_count

        examined_count = self.examined_count

        valid_count = self.valid_count

        invalid_count = self.invalid_count

        unreadable_count = self.unreadable_count

        truncated = self.truncated

        history_complete = self.history_complete

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        from_: str | Unset = UNSET
        if not isinstance(self.from_, Unset):
            from_ = self.from_.isoformat()

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        cutoff_at: str | Unset = UNSET
        if not isinstance(self.cutoff_at, Unset):
            cutoff_at = self.cutoff_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "requested": requested,
                "scanned_count": scanned_count,
                "examined_count": examined_count,
                "valid_count": valid_count,
                "invalid_count": invalid_count,
                "unreadable_count": unreadable_count,
                "truncated": truncated,
                "history_complete": history_complete,
                "results": results,
            }
        )
        if from_ is not UNSET:
            field_dict["from"] = from_
        if until is not UNSET:
            field_dict["until"] = until
        if cutoff_at is not UNSET:
            field_dict["cutoff_at"] = cutoff_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_schema_rollout_validation import EventSchemaRolloutValidation

        d = dict(src_dict)
        requested = d.pop("requested")

        scanned_count = d.pop("scanned_count")

        examined_count = d.pop("examined_count")

        valid_count = d.pop("valid_count")

        invalid_count = d.pop("invalid_count")

        unreadable_count = d.pop("unreadable_count")

        truncated = d.pop("truncated")

        history_complete = d.pop("history_complete")

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = EventSchemaRolloutValidation.from_dict(results_item_data)

            results.append(results_item)

        _from_ = d.pop("from", UNSET)
        from_: datetime.datetime | Unset
        if isinstance(_from_, Unset):
            from_ = UNSET
        else:
            from_ = datetime.datetime.fromisoformat(_from_)

        _until = d.pop("until", UNSET)
        until: datetime.datetime | Unset
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = datetime.datetime.fromisoformat(_until)

        _cutoff_at = d.pop("cutoff_at", UNSET)
        cutoff_at: datetime.datetime | Unset
        if isinstance(_cutoff_at, Unset):
            cutoff_at = UNSET
        else:
            cutoff_at = datetime.datetime.fromisoformat(_cutoff_at)

        event_schema_rollout_retained = cls(
            requested=requested,
            scanned_count=scanned_count,
            examined_count=examined_count,
            valid_count=valid_count,
            invalid_count=invalid_count,
            unreadable_count=unreadable_count,
            truncated=truncated,
            history_complete=history_complete,
            results=results,
            from_=from_,
            until=until,
            cutoff_at=cutoff_at,
        )

        return event_schema_rollout_retained
