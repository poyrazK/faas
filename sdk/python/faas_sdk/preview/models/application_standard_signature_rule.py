from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.application_standard_signature_rule_mode import (
    ApplicationStandardSignatureRuleMode,
    check_application_standard_signature_rule_mode,
)
from ..models.application_standard_signature_rule_override import (
    ApplicationStandardSignatureRuleOverride,
    check_application_standard_signature_rule_override,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardSignatureRule")


@_attrs_define
class ApplicationStandardSignatureRule:
    """Image signature requirement. Modes and override combinations are validated on publication."""

    mode: ApplicationStandardSignatureRuleMode
    value: bool
    override: ApplicationStandardSignatureRuleOverride | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        value = self.value

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
        mode = check_application_standard_signature_rule_mode(d.pop("mode"))

        value = d.pop("value")

        _override = d.pop("override", UNSET)
        override: ApplicationStandardSignatureRuleOverride | Unset
        if isinstance(_override, Unset):
            override = UNSET
        else:
            override = check_application_standard_signature_rule_override(_override)

        application_standard_signature_rule = cls(
            mode=mode,
            value=value,
            override=override,
        )

        return application_standard_signature_rule
