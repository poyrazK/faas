from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.feature_flag_type import FeatureFlagType, check_feature_flag_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_rule import FlagRule
    from ..models.flag_variant import FlagVariant


T = TypeVar("T", bound="FeatureFlag")


@_attrs_define
class FeatureFlag:
    """Application behavior flag; business logic must check it explicitly. Omitted type means boolean for backward
    compatibility.

    """

    key: str
    enabled: bool
    default: bool | str
    """Boolean fallback for boolean flags or named fallback variant for variant flags."""
    rules: list[FlagRule]
    description: str | Unset = UNSET
    type_: FeatureFlagType | Unset = "boolean"
    seed: str | Unset = UNSET
    """Server-supplied stable allocation seed; omit for a new flag."""
    variants: list[FlagVariant] | Unset = UNSET
    """Required for variant flags; weights must total 10000 basis points."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        enabled = self.enabled

        default: bool | str
        default = self.default

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        description = self.description

        type_: str | Unset = UNSET
        if not isinstance(self.type_, Unset):
            type_ = self.type_

        seed = self.seed

        variants: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.variants, Unset):
            variants = []
            for variants_item_data in self.variants:
                variants_item = variants_item_data.to_dict()
                variants.append(variants_item)

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
        if type_ is not UNSET:
            field_dict["type"] = type_
        if seed is not UNSET:
            field_dict["seed"] = seed
        if variants is not UNSET:
            field_dict["variants"] = variants

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_rule import FlagRule
        from ..models.flag_variant import FlagVariant

        d = dict(src_dict)
        key = d.pop("key")

        enabled = d.pop("enabled")

        def _parse_default(data: object) -> bool | str:
            return cast(bool | str, data)

        default = _parse_default(d.pop("default"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = FlagRule.from_dict(rules_item_data)

            rules.append(rules_item)

        description = d.pop("description", UNSET)

        _type_ = d.pop("type", UNSET)
        type_: FeatureFlagType | Unset
        if isinstance(_type_, Unset):
            type_ = UNSET
        else:
            type_ = check_feature_flag_type(_type_)

        seed = d.pop("seed", UNSET)

        _variants = d.pop("variants", UNSET)
        variants: list[FlagVariant] | Unset = UNSET
        if _variants is not UNSET:
            variants = []
            for variants_item_data in _variants:
                variants_item = FlagVariant.from_dict(variants_item_data)

                variants.append(variants_item)

        feature_flag = cls(
            key=key,
            enabled=enabled,
            default=default,
            rules=rules,
            description=description,
            type_=type_,
            seed=seed,
            variants=variants,
        )

        return feature_flag
