from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_platform_flag_decision_reason import (
    OperatorPlatformFlagDecisionReason,
    check_operator_platform_flag_decision_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorPlatformFlagDecision")


@_attrs_define
class OperatorPlatformFlagDecision:
    """One platform flag decided for one account. Available is the decision and the plan entitlement of the released
    capability.

    """

    flag: str
    account_id: UUID
    plan: str
    enabled: bool
    reason: OperatorPlatformFlagDecisionReason
    version: int
    available: bool
    rule_id: str | Unset = UNSET
    bucket: int | Unset = UNSET
    entitled: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flag = self.flag

        account_id = str(self.account_id)

        plan = self.plan

        enabled = self.enabled

        reason: str = self.reason

        version = self.version

        available = self.available

        rule_id = self.rule_id

        bucket = self.bucket

        entitled = self.entitled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flag": flag,
                "account_id": account_id,
                "plan": plan,
                "enabled": enabled,
                "reason": reason,
                "version": version,
                "available": available,
            }
        )
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if bucket is not UNSET:
            field_dict["bucket"] = bucket
        if entitled is not UNSET:
            field_dict["entitled"] = entitled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        flag = d.pop("flag")

        account_id = UUID(d.pop("account_id"))

        plan = d.pop("plan")

        enabled = d.pop("enabled")

        reason = check_operator_platform_flag_decision_reason(d.pop("reason"))

        version = d.pop("version")

        available = d.pop("available")

        rule_id = d.pop("rule_id", UNSET)

        bucket = d.pop("bucket", UNSET)

        entitled = d.pop("entitled", UNSET)

        operator_platform_flag_decision = cls(
            flag=flag,
            account_id=account_id,
            plan=plan,
            enabled=enabled,
            reason=reason,
            version=version,
            available=available,
            rule_id=rule_id,
            bucket=bucket,
            entitled=entitled,
        )

        operator_platform_flag_decision.additional_properties = d
        return operator_platform_flag_decision

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
