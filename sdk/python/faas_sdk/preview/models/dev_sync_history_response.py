from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dev_sync_history_item import DevSyncHistoryItem
    from ..models.dev_sync_history_summary import DevSyncHistorySummary


T = TypeVar("T", bound="DevSyncHistoryResponse")


@_attrs_define
class DevSyncHistoryResponse:
    """Bounded newest-first developer sync history."""

    project: str
    items: list[DevSyncHistoryItem]
    workspace_id: str | Unset = UNSET
    summary: DevSyncHistorySummary | Unset = UNSET
    """Aggregate trend and regression guidance for recent developer syncs."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        workspace_id = self.workspace_id

        summary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.summary, Unset):
            summary = self.summary.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "items": items,
            }
        )
        if workspace_id is not UNSET:
            field_dict["workspace_id"] = workspace_id
        if summary is not UNSET:
            field_dict["summary"] = summary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_sync_history_item import DevSyncHistoryItem
        from ..models.dev_sync_history_summary import DevSyncHistorySummary

        d = dict(src_dict)
        project = d.pop("project")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = DevSyncHistoryItem.from_dict(items_item_data)

            items.append(items_item)

        workspace_id = d.pop("workspace_id", UNSET)

        _summary = d.pop("summary", UNSET)
        summary: DevSyncHistorySummary | Unset
        if isinstance(_summary, Unset):
            summary = UNSET
        else:
            summary = DevSyncHistorySummary.from_dict(_summary)

        dev_sync_history_response = cls(
            project=project,
            items=items,
            workspace_id=workspace_id,
            summary=summary,
        )

        dev_sync_history_response.additional_properties = d
        return dev_sync_history_response

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
