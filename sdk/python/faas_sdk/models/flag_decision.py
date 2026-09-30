from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_decision_reason import FlagDecisionReason, check_flag_decision_reason
from ..models.flag_decision_source import FlagDecisionSource, check_flag_decision_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagDecision")


@_attrs_define
class FlagDecision:
    """Explainable boolean decision against a configuration version."""

    flag: str
    value: bool
    config_version: int
    reason: FlagDecisionReason
    source: FlagDecisionSource
    rule_id: str | Unset = UNSET
    bucket: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flag = self.flag

        value = self.value

        config_version = self.config_version

        reason: str = self.reason

        source: str = self.source

        rule_id = self.rule_id

        bucket = self.bucket

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
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if bucket is not UNSET:
            field_dict["bucket"] = bucket

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        flag = d.pop("flag")

        value = d.pop("value")

        config_version = d.pop("config_version")

        reason = check_flag_decision_reason(d.pop("reason"))

        source = check_flag_decision_source(d.pop("source"))

        rule_id = d.pop("rule_id", UNSET)

        bucket = d.pop("bucket", UNSET)

        flag_decision = cls(
            flag=flag,
            value=value,
            config_version=config_version,
            reason=reason,
            source=source,
            rule_id=rule_id,
            bucket=bucket,
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
