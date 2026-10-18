from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_notification_queue import MCPReadinessNotificationQueue


T = TypeVar("T", bound="MCPReadinessNotificationQueues")


@_attrs_define
class MCPReadinessNotificationQueues:
    """Bounded notification queue page from a consistent database snapshot."""

    observed_at: datetime.datetime
    queues: list[MCPReadinessNotificationQueue]
    next_after: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        queues = []
        for queues_item_data in self.queues:
            queues_item = queues_item_data.to_dict()
            queues.append(queues_item)

        next_after: str | Unset = UNSET
        if not isinstance(self.next_after, Unset):
            next_after = str(self.next_after)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "queues": queues,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_notification_queue import MCPReadinessNotificationQueue

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        queues = []
        _queues = d.pop("queues")
        for queues_item_data in _queues:
            queues_item = MCPReadinessNotificationQueue.from_dict(queues_item_data)

            queues.append(queues_item)

        _next_after = d.pop("next_after", UNSET)
        next_after: UUID | Unset
        if isinstance(_next_after, Unset):
            next_after = UNSET
        else:
            next_after = UUID(_next_after)

        mcp_readiness_notification_queues = cls(
            observed_at=observed_at,
            queues=queues,
            next_after=next_after,
        )

        mcp_readiness_notification_queues.additional_properties = d
        return mcp_readiness_notification_queues

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
