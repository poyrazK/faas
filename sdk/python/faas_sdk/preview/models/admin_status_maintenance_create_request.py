from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.admin_status_maintenance_create_request_components_item import (
    AdminStatusMaintenanceCreateRequestComponentsItem,
    check_admin_status_maintenance_create_request_components_item,
)
from ..models.admin_status_maintenance_create_request_impact import (
    AdminStatusMaintenanceCreateRequestImpact,
    check_admin_status_maintenance_create_request_impact,
)
from ..models.admin_status_maintenance_create_request_kind import (
    AdminStatusMaintenanceCreateRequestKind,
    check_admin_status_maintenance_create_request_kind,
)
from ..models.admin_status_maintenance_create_request_state import (
    AdminStatusMaintenanceCreateRequestState,
    check_admin_status_maintenance_create_request_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AdminStatusMaintenanceCreateRequest")


@_attrs_define
class AdminStatusMaintenanceCreateRequest:
    """Schedule maintenance with a bounded start/end window."""

    kind: AdminStatusMaintenanceCreateRequestKind
    title: str
    components: list[AdminStatusMaintenanceCreateRequestComponentsItem]
    scheduled_start_at: datetime.datetime
    scheduled_end_at: datetime.datetime
    message: str
    impact: AdminStatusMaintenanceCreateRequestImpact | Unset = "maintenance"
    state: AdminStatusMaintenanceCreateRequestState | Unset = "scheduled"

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        title = self.title

        components = []
        for components_item_data in self.components:
            components_item: str = components_item_data
            components.append(components_item)

        scheduled_start_at = self.scheduled_start_at.isoformat()

        scheduled_end_at = self.scheduled_end_at.isoformat()

        message = self.message

        impact: str | Unset = UNSET
        if not isinstance(self.impact, Unset):
            impact = self.impact

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "title": title,
                "components": components,
                "scheduled_start_at": scheduled_start_at,
                "scheduled_end_at": scheduled_end_at,
                "message": message,
            }
        )
        if impact is not UNSET:
            field_dict["impact"] = impact
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_admin_status_maintenance_create_request_kind(d.pop("kind"))

        title = d.pop("title")

        components = []
        _components = d.pop("components")
        for components_item_data in _components:
            components_item = check_admin_status_maintenance_create_request_components_item(components_item_data)

            components.append(components_item)

        scheduled_start_at = datetime.datetime.fromisoformat(d.pop("scheduled_start_at"))

        scheduled_end_at = datetime.datetime.fromisoformat(d.pop("scheduled_end_at"))

        message = d.pop("message")

        _impact = d.pop("impact", UNSET)
        impact: AdminStatusMaintenanceCreateRequestImpact | Unset
        if isinstance(_impact, Unset):
            impact = UNSET
        else:
            impact = check_admin_status_maintenance_create_request_impact(_impact)

        _state = d.pop("state", UNSET)
        state: AdminStatusMaintenanceCreateRequestState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_admin_status_maintenance_create_request_state(_state)

        admin_status_maintenance_create_request = cls(
            kind=kind,
            title=title,
            components=components,
            scheduled_start_at=scheduled_start_at,
            scheduled_end_at=scheduled_end_at,
            message=message,
            impact=impact,
            state=state,
        )

        return admin_status_maintenance_create_request
