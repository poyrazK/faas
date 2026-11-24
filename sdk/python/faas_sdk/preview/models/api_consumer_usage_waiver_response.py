from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerUsageWaiverResponse")


@_attrs_define
class APIConsumerUsageWaiverResponse:
    """Approval attached to the statement revision that recorded the waiver."""

    source_statement_id: UUID
    """Latest held statement revision reviewed for this waiver."""
    unit: str
    """App meter being waived in app:<meter> form."""
    model: str
    """Reported alias without an effective price in the selected window."""
    since: datetime.datetime
    """Inclusive UTC-minute start within the source statement period."""
    until: datetime.datetime
    """Exclusive UTC-minute end within the source period. Must be a closed boundary."""
    units: int
    """Exact count of remaining unpriced units for the model and window on the source draft."""
    reason: str
    """Reason for approving zero charge. The API limit is 500 UTF-8 bytes."""
    approved_by: UUID
    """Gregale account ID of the approver."""
    approved_at: datetime.datetime
    """UTC time Gregale recorded the approval."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_statement_id = str(self.source_statement_id)

        unit = self.unit

        model = self.model

        since = self.since.isoformat()

        until = self.until.isoformat()

        units = self.units

        reason = self.reason

        approved_by = str(self.approved_by)

        approved_at = self.approved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_statement_id": source_statement_id,
                "unit": unit,
                "model": model,
                "since": since,
                "until": until,
                "units": units,
                "reason": reason,
                "approved_by": approved_by,
                "approved_at": approved_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_statement_id = UUID(d.pop("source_statement_id"))

        unit = d.pop("unit")

        model = d.pop("model")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        units = d.pop("units")

        reason = d.pop("reason")

        approved_by = UUID(d.pop("approved_by"))

        approved_at = datetime.datetime.fromisoformat(d.pop("approved_at"))

        api_consumer_usage_waiver_response = cls(
            source_statement_id=source_statement_id,
            unit=unit,
            model=model,
            since=since,
            until=until,
            units=units,
            reason=reason,
            approved_by=approved_by,
            approved_at=approved_at,
        )

        api_consumer_usage_waiver_response.additional_properties = d
        return api_consumer_usage_waiver_response

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
