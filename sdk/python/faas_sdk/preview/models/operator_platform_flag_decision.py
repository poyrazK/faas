from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_platform_flag_decision_opt_in import (
    OperatorPlatformFlagDecisionOptIn,
    check_operator_platform_flag_decision_opt_in,
)
from ..models.operator_platform_flag_decision_reason import (
    OperatorPlatformFlagDecisionReason,
    check_operator_platform_flag_decision_reason,
)
from ..models.operator_platform_flag_decision_schedule_state import (
    OperatorPlatformFlagDecisionScheduleState,
    check_operator_platform_flag_decision_schedule_state,
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
    org_id: UUID | Unset = UNSET
    """Organization the decision was made under, when the inspector was given one."""
    rule_id: str | Unset = UNSET
    bucket: int | Unset = UNSET
    entitled: bool | Unset = UNSET
    scheduled_rule_id: str | Unset = UNSET
    """First rule that matched but was outside its window."""
    schedule_state: OperatorPlatformFlagDecisionScheduleState | Unset = UNSET
    at: datetime.datetime | Unset = UNSET
    """Decision time, when the inspector was asked about another moment than now."""
    opt_in: OperatorPlatformFlagDecisionOptIn | Unset = UNSET
    """Whose self-serve opt-in decided, when one did."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flag = self.flag

        account_id = str(self.account_id)

        plan = self.plan

        enabled = self.enabled

        reason: str = self.reason

        version = self.version

        available = self.available

        org_id: str | Unset = UNSET
        if not isinstance(self.org_id, Unset):
            org_id = str(self.org_id)

        rule_id = self.rule_id

        bucket = self.bucket

        entitled = self.entitled

        scheduled_rule_id = self.scheduled_rule_id

        schedule_state: str | Unset = UNSET
        if not isinstance(self.schedule_state, Unset):
            schedule_state = self.schedule_state

        at: str | Unset = UNSET
        if not isinstance(self.at, Unset):
            at = self.at.isoformat()

        opt_in: str | Unset = UNSET
        if not isinstance(self.opt_in, Unset):
            opt_in = self.opt_in

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
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if bucket is not UNSET:
            field_dict["bucket"] = bucket
        if entitled is not UNSET:
            field_dict["entitled"] = entitled
        if scheduled_rule_id is not UNSET:
            field_dict["scheduled_rule_id"] = scheduled_rule_id
        if schedule_state is not UNSET:
            field_dict["schedule_state"] = schedule_state
        if at is not UNSET:
            field_dict["at"] = at
        if opt_in is not UNSET:
            field_dict["opt_in"] = opt_in

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

        _org_id = d.pop("org_id", UNSET)
        org_id: UUID | Unset
        if isinstance(_org_id, Unset):
            org_id = UNSET
        else:
            org_id = UUID(_org_id)

        rule_id = d.pop("rule_id", UNSET)

        bucket = d.pop("bucket", UNSET)

        entitled = d.pop("entitled", UNSET)

        scheduled_rule_id = d.pop("scheduled_rule_id", UNSET)

        _schedule_state = d.pop("schedule_state", UNSET)
        schedule_state: OperatorPlatformFlagDecisionScheduleState | Unset
        if isinstance(_schedule_state, Unset):
            schedule_state = UNSET
        else:
            schedule_state = check_operator_platform_flag_decision_schedule_state(_schedule_state)

        _at = d.pop("at", UNSET)
        at: datetime.datetime | Unset
        if isinstance(_at, Unset):
            at = UNSET
        else:
            at = datetime.datetime.fromisoformat(_at)

        _opt_in = d.pop("opt_in", UNSET)
        opt_in: OperatorPlatformFlagDecisionOptIn | Unset
        if isinstance(_opt_in, Unset):
            opt_in = UNSET
        else:
            opt_in = check_operator_platform_flag_decision_opt_in(_opt_in)

        operator_platform_flag_decision = cls(
            flag=flag,
            account_id=account_id,
            plan=plan,
            enabled=enabled,
            reason=reason,
            version=version,
            available=available,
            org_id=org_id,
            rule_id=rule_id,
            bucket=bucket,
            entitled=entitled,
            scheduled_rule_id=scheduled_rule_id,
            schedule_state=schedule_state,
            at=at,
            opt_in=opt_in,
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
