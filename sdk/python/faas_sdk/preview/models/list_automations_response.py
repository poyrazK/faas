from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_response import AutomationResponse


T = TypeVar("T", bound="ListAutomationsResponse")


@_attrs_define
class ListAutomationsResponse:
    """App automation definitions, plan limit and execution availability."""

    app_slug: str
    runtime_enabled: bool
    max_definitions: int
    automations: list[AutomationResponse]
    unavailable_reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        runtime_enabled = self.runtime_enabled

        max_definitions = self.max_definitions

        automations = []
        for automations_item_data in self.automations:
            automations_item = automations_item_data.to_dict()
            automations.append(automations_item)

        unavailable_reason = self.unavailable_reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "runtime_enabled": runtime_enabled,
                "max_definitions": max_definitions,
                "automations": automations,
            }
        )
        if unavailable_reason is not UNSET:
            field_dict["unavailable_reason"] = unavailable_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_response import AutomationResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        runtime_enabled = d.pop("runtime_enabled")

        max_definitions = d.pop("max_definitions")

        automations = []
        _automations = d.pop("automations")
        for automations_item_data in _automations:
            automations_item = AutomationResponse.from_dict(automations_item_data)

            automations.append(automations_item)

        unavailable_reason = d.pop("unavailable_reason", UNSET)

        list_automations_response = cls(
            app_slug=app_slug,
            runtime_enabled=runtime_enabled,
            max_definitions=max_definitions,
            automations=automations,
            unavailable_reason=unavailable_reason,
        )

        return list_automations_response
