from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_job_run_request_execution_class import (
    CreateJobRunRequestExecutionClass,
    check_create_job_run_request_execution_class,
)
from ..models.create_job_run_request_failure_policy import (
    CreateJobRunRequestFailurePolicy,
    check_create_job_run_request_failure_policy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_job_run_request_env_overrides import CreateJobRunRequestEnvOverrides
    from ..models.failure_rules import FailureRules
    from ..models.job_run_input import JobRunInput


T = TypeVar("T", bound="CreateJobRunRequest")


@_attrs_define
class CreateJobRunRequest:
    """Atomic fan-out into indexed task records; supply `tasks`, an ordered
    `inputs` array, or an external `input_manifest_uri` and checksum.
    Each manifest entry is assigned to one task index in array order.
    The handler validates the count against `Plan.JobMaxTasksPerRun`
    (Hobby=100, Pro=1000, Scale=5000). Per-run overrides
    (parallelism / retry_max / task_timeout_sec) inherit from
    the job when null.

    """

    tasks: int | Unset = UNSET
    failure_rules: FailureRules | Unset = UNSET
    """Versioned explicit classification policy for failed partition attempts."""
    inputs: list[JobRunInput] | Unset = UNSET
    """Ordered input set. Creates one task per entry; tasks may be omitted or must match the input count.
    References are opaque and fetched by the customer image."""
    input_manifest_uri: str | Unset = UNSET
    """obj://<app-id>/<bucket-id>/<key> for a JSON array of inputs, up to 16 MiB. Mutually exclusive with inputs."""
    input_manifest_sha256: str | Unset = UNSET
    """SHA-256 of the external manifest's exact bytes; required with input_manifest_uri."""
    execution_class: CreateJobRunRequestExecutionClass | Unset = "standard"
    failure_policy: CreateJobRunRequestFailurePolicy | Unset = "continue"
    eligible_at: datetime.datetime | Unset = UNSET
    """Earliest admission time for flexible tasks; defaults to now."""
    latest_start_at: datetime.datetime | Unset = UNSET
    """Required for flexible runs. Unstarted tasks expire at this time; completion is not guaranteed by this time."""
    parallelism: int | Unset = UNSET
    retry_max: int | Unset = UNSET
    task_timeout_sec: int | Unset = UNSET
    env_overrides: CreateJobRunRequestEnvOverrides | Unset = UNSET
    arguments: list[str] | Unset = UNSET
    """Replace the command arguments for this run, retaining its executable. Empty array removes trailing
    arguments."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tasks = self.tasks

        failure_rules: dict[str, Any] | Unset = UNSET
        if not isinstance(self.failure_rules, Unset):
            failure_rules = self.failure_rules.to_dict()

        inputs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.inputs, Unset):
            inputs = []
            for inputs_item_data in self.inputs:
                inputs_item = inputs_item_data.to_dict()
                inputs.append(inputs_item)

        input_manifest_uri = self.input_manifest_uri

        input_manifest_sha256 = self.input_manifest_sha256

        execution_class: str | Unset = UNSET
        if not isinstance(self.execution_class, Unset):
            execution_class = self.execution_class

        failure_policy: str | Unset = UNSET
        if not isinstance(self.failure_policy, Unset):
            failure_policy = self.failure_policy

        eligible_at: str | Unset = UNSET
        if not isinstance(self.eligible_at, Unset):
            eligible_at = self.eligible_at.isoformat()

        latest_start_at: str | Unset = UNSET
        if not isinstance(self.latest_start_at, Unset):
            latest_start_at = self.latest_start_at.isoformat()

        parallelism = self.parallelism

        retry_max = self.retry_max

        task_timeout_sec = self.task_timeout_sec

        env_overrides: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_overrides, Unset):
            env_overrides = self.env_overrides.to_dict()

        arguments: list[str] | Unset = UNSET
        if not isinstance(self.arguments, Unset):
            arguments = self.arguments

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if tasks is not UNSET:
            field_dict["tasks"] = tasks
        if failure_rules is not UNSET:
            field_dict["failure_rules"] = failure_rules
        if inputs is not UNSET:
            field_dict["inputs"] = inputs
        if input_manifest_uri is not UNSET:
            field_dict["input_manifest_uri"] = input_manifest_uri
        if input_manifest_sha256 is not UNSET:
            field_dict["input_manifest_sha256"] = input_manifest_sha256
        if execution_class is not UNSET:
            field_dict["execution_class"] = execution_class
        if failure_policy is not UNSET:
            field_dict["failure_policy"] = failure_policy
        if eligible_at is not UNSET:
            field_dict["eligible_at"] = eligible_at
        if latest_start_at is not UNSET:
            field_dict["latest_start_at"] = latest_start_at
        if parallelism is not UNSET:
            field_dict["parallelism"] = parallelism
        if retry_max is not UNSET:
            field_dict["retry_max"] = retry_max
        if task_timeout_sec is not UNSET:
            field_dict["task_timeout_sec"] = task_timeout_sec
        if env_overrides is not UNSET:
            field_dict["env_overrides"] = env_overrides
        if arguments is not UNSET:
            field_dict["arguments"] = arguments

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_job_run_request_env_overrides import CreateJobRunRequestEnvOverrides
        from ..models.failure_rules import FailureRules
        from ..models.job_run_input import JobRunInput

        d = dict(src_dict)
        tasks = d.pop("tasks", UNSET)

        _failure_rules = d.pop("failure_rules", UNSET)
        failure_rules: FailureRules | Unset
        if isinstance(_failure_rules, Unset):
            failure_rules = UNSET
        else:
            failure_rules = FailureRules.from_dict(_failure_rules)

        _inputs = d.pop("inputs", UNSET)
        inputs: list[JobRunInput] | Unset = UNSET
        if _inputs is not UNSET:
            inputs = []
            for inputs_item_data in _inputs:
                inputs_item = JobRunInput.from_dict(inputs_item_data)

                inputs.append(inputs_item)

        input_manifest_uri = d.pop("input_manifest_uri", UNSET)

        input_manifest_sha256 = d.pop("input_manifest_sha256", UNSET)

        _execution_class = d.pop("execution_class", UNSET)
        execution_class: CreateJobRunRequestExecutionClass | Unset
        if isinstance(_execution_class, Unset):
            execution_class = UNSET
        else:
            execution_class = check_create_job_run_request_execution_class(_execution_class)

        _failure_policy = d.pop("failure_policy", UNSET)
        failure_policy: CreateJobRunRequestFailurePolicy | Unset
        if isinstance(_failure_policy, Unset):
            failure_policy = UNSET
        else:
            failure_policy = check_create_job_run_request_failure_policy(_failure_policy)

        _eligible_at = d.pop("eligible_at", UNSET)
        eligible_at: datetime.datetime | Unset
        if isinstance(_eligible_at, Unset):
            eligible_at = UNSET
        else:
            eligible_at = datetime.datetime.fromisoformat(_eligible_at)

        _latest_start_at = d.pop("latest_start_at", UNSET)
        latest_start_at: datetime.datetime | Unset
        if isinstance(_latest_start_at, Unset):
            latest_start_at = UNSET
        else:
            latest_start_at = datetime.datetime.fromisoformat(_latest_start_at)

        parallelism = d.pop("parallelism", UNSET)

        retry_max = d.pop("retry_max", UNSET)

        task_timeout_sec = d.pop("task_timeout_sec", UNSET)

        _env_overrides = d.pop("env_overrides", UNSET)
        env_overrides: CreateJobRunRequestEnvOverrides | Unset
        if isinstance(_env_overrides, Unset):
            env_overrides = UNSET
        else:
            env_overrides = CreateJobRunRequestEnvOverrides.from_dict(_env_overrides)

        arguments = cast(list[str], d.pop("arguments", UNSET))

        create_job_run_request = cls(
            tasks=tasks,
            failure_rules=failure_rules,
            inputs=inputs,
            input_manifest_uri=input_manifest_uri,
            input_manifest_sha256=input_manifest_sha256,
            execution_class=execution_class,
            failure_policy=failure_policy,
            eligible_at=eligible_at,
            latest_start_at=latest_start_at,
            parallelism=parallelism,
            retry_max=retry_max,
            task_timeout_sec=task_timeout_sec,
            env_overrides=env_overrides,
            arguments=arguments,
        )

        create_job_run_request.additional_properties = d
        return create_job_run_request

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
