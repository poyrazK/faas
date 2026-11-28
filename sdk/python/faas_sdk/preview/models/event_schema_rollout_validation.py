from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSchemaRolloutValidation")


@_attrs_define
class EventSchemaRolloutValidation:
    """Candidate schema validation results for bounded retained event content."""

    valid: bool
    sample_index: int | Unset = UNSET
    """Zero-based index for caller-supplied samples."""
    event_id: str | Unset = UNSET
    """Identity for retained sample results."""
    accepted_at: datetime.datetime | Unset = UNSET
    reason: str | Unset = UNSET
    """Bounded validation category or unreadable_envelope; no payload values are returned."""
    field: str | Unset = UNSET
    """First failing field path, bounded to 256 UTF-8 bytes."""

    def to_dict(self) -> dict[str, Any]:
        valid = self.valid

        sample_index = self.sample_index

        event_id = self.event_id

        accepted_at: str | Unset = UNSET
        if not isinstance(self.accepted_at, Unset):
            accepted_at = self.accepted_at.isoformat()

        reason = self.reason

        field = self.field

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "valid": valid,
            }
        )
        if sample_index is not UNSET:
            field_dict["sample_index"] = sample_index
        if event_id is not UNSET:
            field_dict["event_id"] = event_id
        if accepted_at is not UNSET:
            field_dict["accepted_at"] = accepted_at
        if reason is not UNSET:
            field_dict["reason"] = reason
        if field is not UNSET:
            field_dict["field"] = field

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        valid = d.pop("valid")

        sample_index = d.pop("sample_index", UNSET)

        event_id = d.pop("event_id", UNSET)

        _accepted_at = d.pop("accepted_at", UNSET)
        accepted_at: datetime.datetime | Unset
        if isinstance(_accepted_at, Unset):
            accepted_at = UNSET
        else:
            accepted_at = datetime.datetime.fromisoformat(_accepted_at)

        reason = d.pop("reason", UNSET)

        field = d.pop("field", UNSET)

        event_schema_rollout_validation = cls(
            valid=valid,
            sample_index=sample_index,
            event_id=event_id,
            accepted_at=accepted_at,
            reason=reason,
            field=field,
        )

        return event_schema_rollout_validation
