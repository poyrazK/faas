from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_job_request_kind import CreateJobRequestKind, check_create_job_request_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_job_request_env_overrides import CreateJobRequestEnvOverrides


T = TypeVar("T", bound="CreateJobRequest")


@_attrs_define
class CreateJobRequest:
    """Job creation payload — name + image + command + caps; schedule enables recurring runs."""

    name: str
    image_ref: str
    command: list[str]
    kind: CreateJobRequestKind | Unset = "batch"
    schedule: str | Unset = UNSET
    """Optional five-field cron expression. When present, the job becomes recurring and schedd creates one single-
    task run per occurrence."""
    timezone: str | Unset = UNSET
    """IANA timezone for schedule; defaults to UTC."""
    env_overrides: CreateJobRequestEnvOverrides | Unset = UNSET
    ram_mb: int | Unset = UNSET
    task_timeout_sec: int | Unset = UNSET
    max_parallelism: int | Unset = UNSET
    retry_max: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        image_ref = self.image_ref

        command = self.command

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        schedule = self.schedule

        timezone = self.timezone

        env_overrides: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_overrides, Unset):
            env_overrides = self.env_overrides.to_dict()

        ram_mb = self.ram_mb

        task_timeout_sec = self.task_timeout_sec

        max_parallelism = self.max_parallelism

        retry_max = self.retry_max

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "image_ref": image_ref,
                "command": command,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_job_request_env_overrides import CreateJobRequestEnvOverrides

        d = dict(src_dict)
        name = d.pop("name")

        image_ref = d.pop("image_ref")

        command = cast(list[str], d.pop("command"))

        _kind = d.pop("kind", UNSET)
        kind: CreateJobRequestKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_create_job_request_kind(_kind)

        schedule = d.pop("schedule", UNSET)

        timezone = d.pop("timezone", UNSET)

        _env_overrides = d.pop("env_overrides", UNSET)
        env_overrides: CreateJobRequestEnvOverrides | Unset
        if isinstance(_env_overrides, Unset):
            env_overrides = UNSET
        else:
            env_overrides = CreateJobRequestEnvOverrides.from_dict(_env_overrides)

        ram_mb = d.pop("ram_mb", UNSET)

        task_timeout_sec = d.pop("task_timeout_sec", UNSET)

        max_parallelism = d.pop("max_parallelism", UNSET)

        retry_max = d.pop("retry_max", UNSET)

        create_job_request = cls(
            name=name,
            image_ref=image_ref,
            command=command,
            kind=kind,
            schedule=schedule,
            timezone=timezone,
            env_overrides=env_overrides,
            ram_mb=ram_mb,
            task_timeout_sec=task_timeout_sec,
            max_parallelism=max_parallelism,
            retry_max=retry_max,
        )

        create_job_request.additional_properties = d
        return create_job_request

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
