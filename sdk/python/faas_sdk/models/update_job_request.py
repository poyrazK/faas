from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_job_request_status import UpdateJobRequestStatus, check_update_job_request_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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
