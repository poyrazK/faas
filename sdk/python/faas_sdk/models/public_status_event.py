from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.public_status_event_components_item import (
    PublicStatusEventComponentsItem,
    check_public_status_event_components_item,
)
from ..models.public_status_event_impact import PublicStatusEventImpact, check_public_status_event_impact
from ..models.public_status_event_kind import PublicStatusEventKind, check_public_status_event_kind
from ..models.public_status_event_state import PublicStatusEventState, check_public_status_event_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.public_status_update import PublicStatusUpdate


T = TypeVar("T", bound="PublicStatusEvent")


@_attrs_define
class PublicStatusEvent:
    """Public incident or maintenance metadata and chronological timeline."""

    id: UUID
    kind: PublicStatusEventKind
    title: str
    impact: PublicStatusEventImpact
    components: list[PublicStatusEventComponentsItem]
    state: PublicStatusEventState
    updated_at: datetime.datetime
    updates: list[PublicStatusUpdate]
    starts_at: datetime.datetime | Unset = UNSET
    scheduled_start_at: datetime.datetime | Unset = UNSET
    scheduled_end_at: datetime.datetime | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        kind: str = self.kind

        title = self.title

        impact: str = self.impact

        components = []
        for components_item_data in self.components:
            components_item: str = components_item_data
            components.append(components_item)

        state: str = self.state

        updated_at = self.updated_at.isoformat()

        updates = []
        for updates_item_data in self.updates:
            updates_item = updates_item_data.to_dict()
            updates.append(updates_item)

        starts_at: str | Unset = UNSET
        if not isinstance(self.starts_at, Unset):
            starts_at = self.starts_at.isoformat()

        scheduled_start_at: str | Unset = UNSET
        if not isinstance(self.scheduled_start_at, Unset):
            scheduled_start_at = self.scheduled_start_at.isoformat()

        scheduled_end_at: str | Unset = UNSET
        if not isinstance(self.scheduled_end_at, Unset):
            scheduled_end_at = self.scheduled_end_at.isoformat()

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "title": title,
                "impact": impact,
                "components": components,
                "state": state,
                "updated_at": updated_at,
                "updates": updates,
            }
        )
        if starts_at is not UNSET:
            field_dict["starts_at"] = starts_at
        if scheduled_start_at is not UNSET:
            field_dict["scheduled_start_at"] = scheduled_start_at
        if scheduled_end_at is not UNSET:
            field_dict["scheduled_end_at"] = scheduled_end_at
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.public_status_update import PublicStatusUpdate

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        kind = check_public_status_event_kind(d.pop("kind"))

        title = d.pop("title")

        impact = check_public_status_event_impact(d.pop("impact"))

        components = []
        _components = d.pop("components")
        for components_item_data in _components:
            components_item = check_public_status_event_components_item(components_item_data)

            components.append(components_item)

        state = check_public_status_event_state(d.pop("state"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        updates = []
        _updates = d.pop("updates")
        for updates_item_data in _updates:
            updates_item = PublicStatusUpdate.from_dict(updates_item_data)

            updates.append(updates_item)

        _starts_at = d.pop("starts_at", UNSET)
        starts_at: datetime.datetime | Unset
        if isinstance(_starts_at, Unset):
            starts_at = UNSET
        else:
            starts_at = datetime.datetime.fromisoformat(_starts_at)

        _scheduled_start_at = d.pop("scheduled_start_at", UNSET)
        scheduled_start_at: datetime.datetime | Unset
        if isinstance(_scheduled_start_at, Unset):
            scheduled_start_at = UNSET
        else:
            scheduled_start_at = datetime.datetime.fromisoformat(_scheduled_start_at)

        _scheduled_end_at = d.pop("scheduled_end_at", UNSET)
        scheduled_end_at: datetime.datetime | Unset
        if isinstance(_scheduled_end_at, Unset):
            scheduled_end_at = UNSET
        else:
            scheduled_end_at = datetime.datetime.fromisoformat(_scheduled_end_at)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        public_status_event = cls(
            id=id,
            kind=kind,
            title=title,
            impact=impact,
            components=components,
            state=state,
            updated_at=updated_at,
            updates=updates,
            starts_at=starts_at,
            scheduled_start_at=scheduled_start_at,
            scheduled_end_at=scheduled_end_at,
            resolved_at=resolved_at,
        )

        return public_status_event
