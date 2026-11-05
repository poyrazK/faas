from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_decision_reason import FlagDecisionReason, check_flag_decision_reason
from ..models.flag_decision_source import FlagDecisionSource, check_flag_decision_source
from ..models.flag_decision_type import FlagDecisionType, check_flag_decision_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_decision_inherited_from import FlagDecisionInheritedFrom


T = TypeVar("T", bound="FlagDecision")


@_attrs_define
class FlagDecision:
    """Explainable boolean or named-variant decision against a configuration version."""

    flag: str
    value: bool | str
    config_version: int
    reason: FlagDecisionReason
    source: FlagDecisionSource
    type_: FlagDecisionType | Unset = UNSET
    """Present as variant for named-variant decisions; omitted for legacy boolean decisions."""
    rule_id: str | Unset = UNSET
    bucket: int | Unset = UNSET
    """Boolean rollout bucket or weighted variant assignment bucket."""
    rollout_bucket: int | Unset = UNSET
    """Eligibility bucket for rollout-gated variant rules."""
    inherited_from: FlagDecisionInheritedFrom | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flag = self.flag

        value: bool | str
        value = self.value

        config_version = self.config_version

        reason: str = self.reason

        source: str = self.source

        type_: str | Unset = UNSET
        if not isinstance(self.type_, Unset):
            type_ = self.type_

        rule_id = self.rule_id

        bucket = self.bucket

        rollout_bucket = self.rollout_bucket

        inherited_from: dict[str, Any] | Unset = UNSET
        if not isinstance(self.inherited_from, Unset):
            inherited_from = self.inherited_from.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flag": flag,
                "value": value,
                "config_version": config_version,
                "reason": reason,
                "source": source,
            }
        )
        if type_ is not UNSET:
            field_dict["type"] = type_
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if bucket is not UNSET:
            field_dict["bucket"] = bucket
        if rollout_bucket is not UNSET:
            field_dict["rollout_bucket"] = rollout_bucket
        if inherited_from is not UNSET:
            field_dict["inherited_from"] = inherited_from

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_decision_inherited_from import FlagDecisionInheritedFrom

        d = dict(src_dict)
        flag = d.pop("flag")

        def _parse_value(data: object) -> bool | str:
            return cast(bool | str, data)

        value = _parse_value(d.pop("value"))

        config_version = d.pop("config_version")

        reason = check_flag_decision_reason(d.pop("reason"))

        source = check_flag_decision_source(d.pop("source"))

        _type_ = d.pop("type", UNSET)
        type_: FlagDecisionType | Unset
        if isinstance(_type_, Unset):
            type_ = UNSET
        else:
            type_ = check_flag_decision_type(_type_)

        rule_id = d.pop("rule_id", UNSET)

        bucket = d.pop("bucket", UNSET)

        rollout_bucket = d.pop("rollout_bucket", UNSET)

        _inherited_from = d.pop("inherited_from", UNSET)
        inherited_from: FlagDecisionInheritedFrom | Unset
        if isinstance(_inherited_from, Unset):
            inherited_from = UNSET
        else:
            inherited_from = FlagDecisionInheritedFrom.from_dict(_inherited_from)

        flag_decision = cls(
            flag=flag,
            value=value,
            config_version=config_version,
            reason=reason,
            source=source,
            type_=type_,
            rule_id=rule_id,
            bucket=bucket,
            rollout_bucket=rollout_bucket,
            inherited_from=inherited_from,
        )

        flag_decision.additional_properties = d
        return flag_decision

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
