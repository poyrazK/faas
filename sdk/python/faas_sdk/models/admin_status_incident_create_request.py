from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.admin_status_incident_create_request_components_item import (
    AdminStatusIncidentCreateRequestComponentsItem,
    check_admin_status_incident_create_request_components_item,
)
from ..models.admin_status_incident_create_request_impact import (
    AdminStatusIncidentCreateRequestImpact,
    check_admin_status_incident_create_request_impact,
)
from ..models.admin_status_incident_create_request_kind import (
    AdminStatusIncidentCreateRequestKind,
    check_admin_status_incident_create_request_kind,
)
from ..models.admin_status_incident_create_request_state import (
    AdminStatusIncidentCreateRequestState,
    check_admin_status_incident_create_request_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AdminStatusIncidentCreateRequest")


@_attrs_define
class AdminStatusIncidentCreateRequest:
    """Publish an active incident. Terminal and maintenance lifecycle states are not valid at creation."""

    kind: AdminStatusIncidentCreateRequestKind
    title: str
    impact: AdminStatusIncidentCreateRequestImpact
    components: list[AdminStatusIncidentCreateRequestComponentsItem]
    message: str
    state: AdminStatusIncidentCreateRequestState | Unset = "investigating"
    starts_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        title = self.title

        impact: str = self.impact

        components = []
        for components_item_data in self.components:
            components_item: str = components_item_data
            components.append(components_item)

        message = self.message

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        starts_at: str | Unset = UNSET
        if not isinstance(self.starts_at, Unset):
            starts_at = self.starts_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "title": title,
                "impact": impact,
                "components": components,
                "message": message,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state
        if starts_at is not UNSET:
            field_dict["starts_at"] = starts_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_admin_status_incident_create_request_kind(d.pop("kind"))

        title = d.pop("title")

        impact = check_admin_status_incident_create_request_impact(d.pop("impact"))

        components = []
        _components = d.pop("components")
        for components_item_data in _components:
            components_item = check_admin_status_incident_create_request_components_item(components_item_data)

            components.append(components_item)

        message = d.pop("message")

        _state = d.pop("state", UNSET)
        state: AdminStatusIncidentCreateRequestState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_admin_status_incident_create_request_state(_state)

        _starts_at = d.pop("starts_at", UNSET)
        starts_at: datetime.datetime | Unset
        if isinstance(_starts_at, Unset):
            starts_at = UNSET
        else:
            starts_at = datetime.datetime.fromisoformat(_starts_at)

        admin_status_incident_create_request = cls(
            kind=kind,
            title=title,
            impact=impact,
            components=components,
            message=message,
            state=state,
            starts_at=starts_at,
        )

        return admin_status_incident_create_request
