from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.failure_rules import FailureRules
    from ..models.schedule_policy import SchedulePolicy


T = TypeVar("T", bound="EnvironmentJobSchedule")


@_attrs_define
class EnvironmentJobSchedule:
    """Reviewed recurring schedule for a job workload. Cron and timezone map to Gregale's durable Job schedule; production
    dispatch remains gated until the managed Job adapter is available.

    """

    cron: str
    """Five-field cron expression evaluated in timezone."""
    timezone: str | Unset = "UTC"
    """IANA timezone. Empty or omitted uses UTC."""
    schedule_policy: SchedulePolicy | Unset = UNSET
    """Versioned recurring-work scheduling policy for Jobs and both HTTP and command Crons. HTTP replace waits for
    a prior dispatched request to complete because the scheduler has no stop acknowledgement for a request already
    delivered to the app."""
    failure_rules: FailureRules | Unset = UNSET
    """Versioned explicit classification policy for failed Job partitions, command-Cron executions, and HTTP Cron
    outcome codes. HTTP status is not a business outcome matcher."""

    def to_dict(self) -> dict[str, Any]:
        cron = self.cron

        timezone = self.timezone

        schedule_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.schedule_policy, Unset):
            schedule_policy = self.schedule_policy.to_dict()

        failure_rules: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure_rules, Unset):
            failure_rules = self.failure_rules.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cron": cron,
            }
        )
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
        if schedule_policy is not UNSET:
            field_dict["schedule_policy"] = schedule_policy
        if failure_rules is not UNSET:
            field_dict["failure_rules"] = failure_rules

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.failure_rules import FailureRules
        from ..models.schedule_policy import SchedulePolicy

        d = dict(src_dict)
        cron = d.pop("cron")

        timezone = d.pop("timezone", UNSET)

        _schedule_policy = d.pop("schedule_policy", UNSET)
        schedule_policy: SchedulePolicy | Unset
        if isinstance(_schedule_policy, Unset):
            schedule_policy = UNSET
        else:
            schedule_policy = SchedulePolicy.from_dict(_schedule_policy)

        _failure_rules = d.pop("failure_rules", UNSET)
        failure_rules: FailureRules | Unset
        if isinstance(_failure_rules, Unset):
            failure_rules = UNSET
        else:
            failure_rules = FailureRules.from_dict(_failure_rules)

        environment_job_schedule = cls(
            cron=cron,
            timezone=timezone,
            schedule_policy=schedule_policy,
            failure_rules=failure_rules,
        )

        return environment_job_schedule
