from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_alert_triage_history_coverage import ObsAlertTriageHistoryCoverage
    from ..models.obs_alert_triage_history_entry import ObsAlertTriageHistoryEntry


T = TypeVar("T", bound="ObsAlertTriageHistory")


@_attrs_define
class ObsAlertTriageHistory:
    """One episode-bound page of workflow receipts with a fixed upper revision and explicit retention gaps."""

    incident_id: str
    generated_at: datetime.datetime
    snapshot_at: datetime.datetime
    """First page clock retained across cursor requests; it is not a persistent database snapshot."""
    snapshot_revision: int
    current_revision: int
    """Current episode revision in this page read; can exceed snapshot_revision on subsequent pages."""
    items: list[ObsAlertTriageHistoryEntry]
    limit: int
    truncated: bool
    next_cursor: str
    history_path: str
    coverage: ObsAlertTriageHistoryCoverage
    """Retained receipt counts across revisions one through snapshot_revision independent of page truncation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        incident_id = self.incident_id

        generated_at = self.generated_at.isoformat()

        snapshot_at = self.snapshot_at.isoformat()

        snapshot_revision = self.snapshot_revision

        current_revision = self.current_revision

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        limit = self.limit

        truncated = self.truncated

        next_cursor = self.next_cursor

        history_path = self.history_path

        coverage = self.coverage.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "incident_id": incident_id,
                "generated_at": generated_at,
                "snapshot_at": snapshot_at,
                "snapshot_revision": snapshot_revision,
                "current_revision": current_revision,
                "items": items,
                "limit": limit,
                "truncated": truncated,
                "next_cursor": next_cursor,
                "history_path": history_path,
                "coverage": coverage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_triage_history_coverage import ObsAlertTriageHistoryCoverage
        from ..models.obs_alert_triage_history_entry import ObsAlertTriageHistoryEntry

        d = dict(src_dict)
        incident_id = d.pop("incident_id")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        snapshot_at = datetime.datetime.fromisoformat(d.pop("snapshot_at"))

        snapshot_revision = d.pop("snapshot_revision")

        current_revision = d.pop("current_revision")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsAlertTriageHistoryEntry.from_dict(items_item_data)

            items.append(items_item)

        limit = d.pop("limit")

        truncated = d.pop("truncated")

        next_cursor = d.pop("next_cursor")

        history_path = d.pop("history_path")

        coverage = ObsAlertTriageHistoryCoverage.from_dict(d.pop("coverage"))

        obs_alert_triage_history = cls(
            incident_id=incident_id,
            generated_at=generated_at,
            snapshot_at=snapshot_at,
            snapshot_revision=snapshot_revision,
            current_revision=current_revision,
            items=items,
            limit=limit,
            truncated=truncated,
            next_cursor=next_cursor,
            history_path=history_path,
            coverage=coverage,
        )

        obs_alert_triage_history.additional_properties = d
        return obs_alert_triage_history

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
