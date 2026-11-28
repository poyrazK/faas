from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_authorization_check_result import (
    ServiceAuthorizationCheckResult,
    check_service_authorization_check_result,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ServiceAuthorizationCheck")


@_attrs_define
class ServiceAuthorizationCheck:
    """Stable rule and sanitized reason from a service policy observation."""

    rule: str
    result: ServiceAuthorizationCheckResult
    reason: str
    setting: str | Unset = UNSET
    hint: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rule = self.rule

        result: str = self.result

        reason = self.reason

        setting = self.setting

        hint = self.hint

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule": rule,
                "result": result,
                "reason": reason,
            }
        )
        if setting is not UNSET:
            field_dict["setting"] = setting
        if hint is not UNSET:
            field_dict["hint"] = hint

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rule = d.pop("rule")

        result = check_service_authorization_check_result(d.pop("result"))

        reason = d.pop("reason")

        setting = d.pop("setting", UNSET)

        hint = d.pop("hint", UNSET)

        service_authorization_check = cls(
            rule=rule,
            result=result,
            reason=reason,
            setting=setting,
            hint=hint,
        )

        service_authorization_check.additional_properties = d
        return service_authorization_check

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
