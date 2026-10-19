from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_flag_rule import PlatformFlagRule


T = TypeVar("T", bound="PlatformFlagDefinition")


@_attrs_define
class PlatformFlagDefinition:
    """Stored value of a platform flag. Enabled is the kill switch; default applies to accounts no rule matches."""

    enabled: bool
    default: bool
    rules: list[PlatformFlagRule] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        default = self.default

        rules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.rules, Unset):
            rules = []
            for rules_item_data in self.rules:
                rules_item = rules_item_data.to_dict()
                rules.append(rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "default": default,
            }
        )
        if rules is not UNSET:
            field_dict["rules"] = rules

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_flag_rule import PlatformFlagRule

        d = dict(src_dict)
        enabled = d.pop("enabled")

        default = d.pop("default")

        _rules = d.pop("rules", UNSET)
        rules: list[PlatformFlagRule] | Unset = UNSET
        if _rules is not UNSET:
            rules = []
            for rules_item_data in _rules:
                rules_item = PlatformFlagRule.from_dict(rules_item_data)

                rules.append(rules_item)

        platform_flag_definition = cls(
            enabled=enabled,
            default=default,
            rules=rules,
        )

        platform_flag_definition.additional_properties = d
        return platform_flag_definition

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
