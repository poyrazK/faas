from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_rule import FlagRule


T = TypeVar("T", bound="FeatureFlag")


@_attrs_define
class FeatureFlag:
    """Boolean application behavior flag; business logic must check it explicitly."""

    key: str
    enabled: bool
    default: bool
    """Value for disabled flags and unmatched customer rules."""
    rules: list[FlagRule]
    description: str | Unset = UNSET
    seed: str | Unset = UNSET
    """Server-supplied stable allocation seed; omit for a new flag."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        enabled = self.enabled

        default = self.default

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        description = self.description

        seed = self.seed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "enabled": enabled,
                "default": default,
                "rules": rules,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description
        if seed is not UNSET:
            field_dict["seed"] = seed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_rule import FlagRule

        d = dict(src_dict)
        key = d.pop("key")

        enabled = d.pop("enabled")

        default = d.pop("default")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = FlagRule.from_dict(rules_item_data)

            rules.append(rules_item)

        description = d.pop("description", UNSET)

        seed = d.pop("seed", UNSET)

        feature_flag = cls(
            key=key,
            enabled=enabled,
            default=default,
            rules=rules,
            description=description,
            seed=seed,
        )

        return feature_flag
