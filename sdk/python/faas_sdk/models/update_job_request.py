from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_job_request_status import UpdateJobRequestStatus, check_update_job_request_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.failure_rules import FailureRules
    from ..models.schedule_policy import SchedulePolicy
    from ..models.update_job_request_env_overrides import UpdateJobRequestEnvOverrides


T = TypeVar("T", bound="UpdateJobRequest")


@_attrs_define
class UpdateJobRequest:
    """Partial job update. nil pointer fields leave the column untouched."""

    image_ref: str | Unset = UNSET
    command: list[str] | Unset = UNSET
    env_overrides: UpdateJobRequestEnvOverrides | Unset = UNSET
    ram_mb: int | Unset = UNSET
    task_timeout_sec: int | Unset = UNSET
    max_parallelism: int | Unset = UNSET
    retry_max: int | Unset = UNSET
    status: UpdateJobRequestStatus | Unset = UNSET
    schedule: str | Unset = UNSET
    """Replace the cron expression; an empty string removes the schedule."""
    timezone: str | Unset = UNSET
    """Replace the schedule IANA timezone."""
    schedule_policy: SchedulePolicy | Unset = UNSET
    """Versioned recurring-work scheduling policy for Jobs and both HTTP and command Crons. HTTP replace waits for
    a prior dispatched request to complete because the scheduler has no stop acknowledgement for a request already
    delivered to the app."""
    failure_rules: FailureRules | Unset = UNSET
    """Versioned explicit classification policy for failed Job partitions, command-Cron executions, and HTTP Cron
    outcome codes. HTTP status is not a business outcome matcher."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        image_ref = self.image_ref

        command: list[str] | Unset = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        env_overrides: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_overrides, Unset):
            env_overrides = self.env_overrides.to_dict()

        ram_mb = self.ram_mb

        task_timeout_sec = self.task_timeout_sec

        max_parallelism = self.max_parallelism

        retry_max = self.retry_max

        status: str | Unset = UNSET
        if not isinstance(self.status, Unset):
            status = self.status

        schedule = self.schedule

        timezone = self.timezone

        schedule_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.schedule_policy, Unset):
            schedule_policy = self.schedule_policy.to_dict()

        failure_rules: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure_rules, Unset):
            failure_rules = self.failure_rules.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if image_ref is not UNSET:
            field_dict["image_ref"] = image_ref
        if command is not UNSET:
            field_dict["command"] = command
        if env_overrides is not UNSET:
            field_dict["env_overrides"] = env_overrides
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if task_timeout_sec is not UNSET:
            field_dict["task_timeout_sec"] = task_timeout_sec
        if max_parallelism is not UNSET:
            field_dict["max_parallelism"] = max_parallelism
        if retry_max is not UNSET:
            field_dict["retry_max"] = retry_max
        if status is not UNSET:
            field_dict["status"] = status
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
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
        from ..models.update_job_request_env_overrides import UpdateJobRequestEnvOverrides

        d = dict(src_dict)
        image_ref = d.pop("image_ref", UNSET)

        command = cast(list[str], d.pop("command", UNSET))

        _env_overrides = d.pop("env_overrides", UNSET)
        env_overrides: UpdateJobRequestEnvOverrides | Unset
        if isinstance(_env_overrides, Unset):
            env_overrides = UNSET
        else:
            env_overrides = UpdateJobRequestEnvOverrides.from_dict(_env_overrides)

        ram_mb = d.pop("ram_mb", UNSET)

        task_timeout_sec = d.pop("task_timeout_sec", UNSET)

        max_parallelism = d.pop("max_parallelism", UNSET)

        retry_max = d.pop("retry_max", UNSET)

        _status = d.pop("status", UNSET)
        status: UpdateJobRequestStatus | Unset
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = check_update_job_request_status(_status)

        schedule = d.pop("schedule", UNSET)

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

        update_job_request = cls(
            image_ref=image_ref,
            command=command,
            env_overrides=env_overrides,
            ram_mb=ram_mb,
            task_timeout_sec=task_timeout_sec,
            max_parallelism=max_parallelism,
            retry_max=retry_max,
            status=status,
            schedule=schedule,
            timezone=timezone,
            schedule_policy=schedule_policy,
            failure_rules=failure_rules,
        )

        update_job_request.additional_properties = d
        return update_job_request

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
