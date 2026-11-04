from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.public_status_update_components_item import (
    PublicStatusUpdateComponentsItem,
    check_public_status_update_components_item,
)
from ..models.public_status_update_impact import PublicStatusUpdateImpact, check_public_status_update_impact
from ..models.public_status_update_state import PublicStatusUpdateState, check_public_status_update_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="PublicStatusUpdate")


@_attrs_define
class PublicStatusUpdate:
    """One chronological event timeline entry. posted_at is immutable; edited_at discloses a later text correction.
    Optional impact and components record a re-rating made with this update.

    """

    id: UUID
    state: PublicStatusUpdateState
    message: str
    posted_at: datetime.datetime
    edited_at: datetime.datetime | Unset = UNSET
    impact: PublicStatusUpdateImpact | Unset = UNSET
    components: list[PublicStatusUpdateComponentsItem] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        state: str = self.state

        message = self.message

        posted_at = self.posted_at.isoformat()

        edited_at: str | Unset = UNSET
        if not isinstance(self.edited_at, Unset):
            edited_at = self.edited_at.isoformat()

        impact: str | Unset = UNSET
        if not isinstance(self.impact, Unset):
            impact = self.impact

        components: list[str] | Unset = UNSET
        if not isinstance(self.components, Unset):
            components = []
            for components_item_data in self.components:
                components_item: str = components_item_data
                components.append(components_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "state": state,
                "message": message,
                "posted_at": posted_at,
            }
        )
        if edited_at is not UNSET:
            field_dict["edited_at"] = edited_at
        if impact is not UNSET:
            field_dict["impact"] = impact
        if components is not UNSET:
            field_dict["components"] = components

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        state = check_public_status_update_state(d.pop("state"))

        message = d.pop("message")

        posted_at = datetime.datetime.fromisoformat(d.pop("posted_at"))

        _edited_at = d.pop("edited_at", UNSET)
        edited_at: datetime.datetime | Unset
        if isinstance(_edited_at, Unset):
            edited_at = UNSET
        else:
            edited_at = datetime.datetime.fromisoformat(_edited_at)

        _impact = d.pop("impact", UNSET)
        impact: PublicStatusUpdateImpact | Unset
        if isinstance(_impact, Unset):
            impact = UNSET
        else:
            impact = check_public_status_update_impact(_impact)

        _components = d.pop("components", UNSET)
        components: list[PublicStatusUpdateComponentsItem] | Unset = UNSET
        if _components is not UNSET:
            components = []
            for components_item_data in _components:
                components_item = check_public_status_update_components_item(components_item_data)

                components.append(components_item)

        public_status_update = cls(
            id=id,
            state=state,
            message=message,
            posted_at=posted_at,
            edited_at=edited_at,
            impact=impact,
            components=components,
        )

        return public_status_update
