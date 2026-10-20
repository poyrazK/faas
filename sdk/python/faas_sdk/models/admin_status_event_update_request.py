from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.admin_status_event_update_request_components_item import (
    AdminStatusEventUpdateRequestComponentsItem,
    check_admin_status_event_update_request_components_item,
)
from ..models.admin_status_event_update_request_impact import (
    AdminStatusEventUpdateRequestImpact,
    check_admin_status_event_update_request_impact,
)
from ..models.admin_status_event_update_request_state import (
    AdminStatusEventUpdateRequestState,
    check_admin_status_event_update_request_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AdminStatusEventUpdateRequest")


@_attrs_define
class AdminStatusEventUpdateRequest:
    """Operator request to append a lifecycle update and optionally re-rate its impact or affected components."""

    state: AdminStatusEventUpdateRequestState
    message: str
    impact: AdminStatusEventUpdateRequestImpact | Unset = UNSET
    components: list[AdminStatusEventUpdateRequestComponentsItem] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        message = self.message

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
                "state": state,
                "message": message,
            }
        )
        if impact is not UNSET:
            field_dict["impact"] = impact
        if components is not UNSET:
            field_dict["components"] = components

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_admin_status_event_update_request_state(d.pop("state"))

        message = d.pop("message")

        _impact = d.pop("impact", UNSET)
        impact: AdminStatusEventUpdateRequestImpact | Unset
        if isinstance(_impact, Unset):
            impact = UNSET
        else:
            impact = check_admin_status_event_update_request_impact(_impact)

        _components = d.pop("components", UNSET)
        components: list[AdminStatusEventUpdateRequestComponentsItem] | Unset = UNSET
        if _components is not UNSET:
            components = []
            for components_item_data in _components:
                components_item = check_admin_status_event_update_request_components_item(components_item_data)

                components.append(components_item)

        admin_status_event_update_request = cls(
            state=state,
            message=message,
            impact=impact,
            components=components,
        )

        return admin_status_event_update_request
