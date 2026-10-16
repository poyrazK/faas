from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_log_destination_rule_mode import (
    ApplicationStandardLogDestinationRuleMode,
    check_application_standard_log_destination_rule_mode,
)
from ..models.application_standard_log_destination_rule_override import (
    ApplicationStandardLogDestinationRuleOverride,
    check_application_standard_log_destination_rule_override,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardLogDestinationRule")


@_attrs_define
class ApplicationStandardLogDestinationRule:
    """Organization-owned logging destination references. Modes and override combinations are validated on publication."""

    mode: ApplicationStandardLogDestinationRuleMode
    value: list[UUID]
    override: ApplicationStandardLogDestinationRuleOverride | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        value = []
        for value_item_data in self.value:
            value_item = str(value_item_data)
            value.append(value_item)

        override: str | Unset = UNSET
        if not isinstance(self.override, Unset):
            override = self.override

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
                "value": value,
            }
        )
        if override is not UNSET:
            field_dict["override"] = override

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_application_standard_log_destination_rule_mode(d.pop("mode"))

        value = []
        _value = d.pop("value")
        for value_item_data in _value:
            value_item = UUID(value_item_data)

            value.append(value_item)

        _override = d.pop("override", UNSET)
        override: ApplicationStandardLogDestinationRuleOverride | Unset
        if isinstance(_override, Unset):
            override = UNSET
        else:
            override = check_application_standard_log_destination_rule_override(_override)

        application_standard_log_destination_rule = cls(
            mode=mode,
            value=value,
            override=override,
        )

        return application_standard_log_destination_rule
