from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.saved_insight_result_kind import SavedInsightResultKind, check_saved_insight_result_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_event_series import InsightEventSeries
    from ..models.insight_funnel import InsightFunnel
    from ..models.insight_retention import InsightRetention


T = TypeVar("T", bound="SavedInsightResult")


@_attrs_define
class SavedInsightResult:
    """One run of a saved insight."""

    key: str
    kind: SavedInsightResultKind
    from_: datetime.datetime
    until: datetime.datetime
    value: float | Unset = UNSET
    """Absent when there is no data: a funnel without entrants, or retention before a period has ended."""
    events: InsightEventSeries | Unset = UNSET
    """A product analytics event series."""
    funnel: InsightFunnel | Unset = UNSET
    """A funnel result."""
    retention: InsightRetention | Unset = UNSET
    """A retention result."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        value = self.value

        events: dict[str, Any] | Unset = UNSET
        if not isinstance(self.events, Unset):
            events = self.events.to_dict()

        funnel: dict[str, Any] | Unset = UNSET
        if not isinstance(self.funnel, Unset):
            funnel = self.funnel.to_dict()

        retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retention, Unset):
            retention = self.retention.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "kind": kind,
                "from": from_,
                "until": until,
            }
        )
        if value is not UNSET:
            field_dict["value"] = value
        if events is not UNSET:
            field_dict["events"] = events
        if funnel is not UNSET:
            field_dict["funnel"] = funnel
        if retention is not UNSET:
            field_dict["retention"] = retention

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_event_series import InsightEventSeries
        from ..models.insight_funnel import InsightFunnel
        from ..models.insight_retention import InsightRetention

        d = dict(src_dict)
        key = d.pop("key")

        kind = check_saved_insight_result_kind(d.pop("kind"))

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        value = d.pop("value", UNSET)

        _events = d.pop("events", UNSET)
        events: InsightEventSeries | Unset
        if isinstance(_events, Unset):
            events = UNSET
        else:
            events = InsightEventSeries.from_dict(_events)

        _funnel = d.pop("funnel", UNSET)
        funnel: InsightFunnel | Unset
        if isinstance(_funnel, Unset):
            funnel = UNSET
        else:
            funnel = InsightFunnel.from_dict(_funnel)

        _retention = d.pop("retention", UNSET)
        retention: InsightRetention | Unset
        if isinstance(_retention, Unset):
            retention = UNSET
        else:
            retention = InsightRetention.from_dict(_retention)

        saved_insight_result = cls(
            key=key,
            kind=kind,
            from_=from_,
            until=until,
            value=value,
            events=events,
            funnel=funnel,
            retention=retention,
        )

        saved_insight_result.additional_properties = d
        return saved_insight_result

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
