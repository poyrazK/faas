from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_response_profile import ExecutionResponseProfile, check_execution_response_profile
from ..models.execution_response_runtime import ExecutionResponseRuntime, check_execution_response_runtime
from ..models.execution_response_status import ExecutionResponseStatus, check_execution_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_artifact import ExecutionArtifact
    from ..models.execution_failure import ExecutionFailure
    from ..models.execution_response_packages import ExecutionResponsePackages
    from ..models.execution_usage import ExecutionUsage
    from ..models.resolved_execution_limits import ResolvedExecutionLimits


T = TypeVar("T", bound="ExecutionResponse")


@_attrs_define
class ExecutionResponse:
    """Disposable execution receipt. Runs-only keys can read only receipts
    created by their key family; broad credentials retain account-wide
    access. Source and input are intentionally omitted. A terminal response is written only after the
    execution VM has been destroyed. Optional workflow metadata is echoed
    as a grouping aid and remains subject to the same ownership boundary.

    """

    id: UUID
    status: ExecutionResponseStatus
    runtime: ExecutionResponseRuntime
    limits: ResolvedExecutionLimits
    """Immutable limits admitted and enforced for one execution."""
    output_truncated: bool
    created_at: datetime.datetime
    profile: ExecutionResponseProfile | Unset = UNSET
    runtime_image_digest: str | Unset = UNSET
    """Scheduler-pinned base image digest, recorded before dispatch of a dependency profile."""
    packages: ExecutionResponsePackages | Unset = UNSET
    """Immutable versions declared by the selected profile and verified by its guest before caller code runs."""
    workflow_id: str | Unset = UNSET
    """Caller-generated workflow grouping id, when assigned."""
    step_label: str | Unset = UNSET
    """Optional step label, when assigned."""
    artifacts: list[ExecutionArtifact] | Unset = UNSET
    """Selected output files, present only after successful execution and VM teardown."""
    result: Any | Unset = UNSET
    """Terminal JSON result, omitted when unavailable."""
    stdout: str | Unset = UNSET
    stderr: str | Unset = UNSET
    exit_code: int | None | Unset = UNSET
    usage: ExecutionUsage | None | Unset = UNSET
    failure: ExecutionFailure | None | Unset = UNSET
    started_at: datetime.datetime | None | Unset = UNSET
    finished_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.execution_failure import ExecutionFailure
        from ..models.execution_usage import ExecutionUsage

        id = str(self.id)

        status: str = self.status

        runtime: str = self.runtime

        limits = self.limits.to_dict()

        output_truncated = self.output_truncated

        created_at = self.created_at.isoformat()

        profile: str | Unset = UNSET
        if not isinstance(self.profile, Unset):
            profile = self.profile

        runtime_image_digest = self.runtime_image_digest

        packages: dict[str, Any] | Unset = UNSET
        if not isinstance(self.packages, Unset):
            packages = self.packages.to_dict()

        workflow_id = self.workflow_id

        step_label = self.step_label

        artifacts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.artifacts, Unset):
            artifacts = []
            for artifacts_item_data in self.artifacts:
                artifacts_item = artifacts_item_data.to_dict()
                artifacts.append(artifacts_item)

        result = self.result

        stdout = self.stdout

        stderr = self.stderr

        exit_code: int | None | Unset
        if isinstance(self.exit_code, Unset):
            exit_code = UNSET
        else:
            exit_code = self.exit_code

        usage: dict[str, Any] | None | Unset
        if isinstance(self.usage, Unset):
            usage = UNSET
        elif isinstance(self.usage, ExecutionUsage):
            usage = self.usage.to_dict()
        else:
            usage = self.usage

        failure: dict[str, Any] | None | Unset
        if isinstance(self.failure, Unset):
            failure = UNSET
        elif isinstance(self.failure, ExecutionFailure):
            failure = self.failure.to_dict()
        else:
            failure = self.failure

        started_at: None | str | Unset
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        finished_at: None | str | Unset
        if isinstance(self.finished_at, Unset):
            finished_at = UNSET
        elif isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
                "runtime": runtime,
                "limits": limits,
                "output_truncated": output_truncated,
                "created_at": created_at,
            }
        )
        if profile is not UNSET:
            field_dict["profile"] = profile
        if runtime_image_digest is not UNSET:
            field_dict["runtime_image_digest"] = runtime_image_digest
        if packages is not UNSET:
            field_dict["packages"] = packages
        if workflow_id is not UNSET:
            field_dict["workflow_id"] = workflow_id
        if step_label is not UNSET:
            field_dict["step_label"] = step_label
        if artifacts is not UNSET:
            field_dict["artifacts"] = artifacts
        if result is not UNSET:
            field_dict["result"] = result
        if stdout is not UNSET:
            field_dict["stdout"] = stdout
        if stderr is not UNSET:
            field_dict["stderr"] = stderr
        if exit_code is not UNSET:
            field_dict["exit_code"] = exit_code
        if usage is not UNSET:
            field_dict["usage"] = usage
        if failure is not UNSET:
            field_dict["failure"] = failure
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_artifact import ExecutionArtifact
        from ..models.execution_failure import ExecutionFailure
        from ..models.execution_response_packages import ExecutionResponsePackages
        from ..models.execution_usage import ExecutionUsage
        from ..models.resolved_execution_limits import ResolvedExecutionLimits

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_execution_response_status(d.pop("status"))

        runtime = check_execution_response_runtime(d.pop("runtime"))

        limits = ResolvedExecutionLimits.from_dict(d.pop("limits"))

        output_truncated = d.pop("output_truncated")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _profile = d.pop("profile", UNSET)
        profile: ExecutionResponseProfile | Unset
        if isinstance(_profile, Unset):
            profile = UNSET
        else:
            profile = check_execution_response_profile(_profile)

        runtime_image_digest = d.pop("runtime_image_digest", UNSET)

        _packages = d.pop("packages", UNSET)
        packages: ExecutionResponsePackages | Unset
        if isinstance(_packages, Unset):
            packages = UNSET
        else:
            packages = ExecutionResponsePackages.from_dict(_packages)

        workflow_id = d.pop("workflow_id", UNSET)

        step_label = d.pop("step_label", UNSET)

        _artifacts = d.pop("artifacts", UNSET)
        artifacts: list[ExecutionArtifact] | Unset = UNSET
        if _artifacts is not UNSET:
            artifacts = []
            for artifacts_item_data in _artifacts:
                artifacts_item = ExecutionArtifact.from_dict(artifacts_item_data)

                artifacts.append(artifacts_item)

        result = d.pop("result", UNSET)

        stdout = d.pop("stdout", UNSET)

        stderr = d.pop("stderr", UNSET)

        def _parse_exit_code(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        exit_code = _parse_exit_code(d.pop("exit_code", UNSET))

        def _parse_usage(data: object) -> ExecutionUsage | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                usage_type_0 = ExecutionUsage.from_dict(data)

                return usage_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(ExecutionUsage | None | Unset, data)

        usage = _parse_usage(d.pop("usage", UNSET))

        def _parse_failure(data: object) -> ExecutionFailure | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                failure_type_0 = ExecutionFailure.from_dict(data)

                return failure_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(ExecutionFailure | None | Unset, data)

        failure = _parse_failure(d.pop("failure", UNSET))

        def _parse_started_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        started_at = _parse_started_at(d.pop("started_at", UNSET))

        def _parse_finished_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finished_at_type_0 = datetime.datetime.fromisoformat(data)

                return finished_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        finished_at = _parse_finished_at(d.pop("finished_at", UNSET))

        execution_response = cls(
            id=id,
            status=status,
            runtime=runtime,
            limits=limits,
            output_truncated=output_truncated,
            created_at=created_at,
            profile=profile,
            runtime_image_digest=runtime_image_digest,
            packages=packages,
            workflow_id=workflow_id,
            step_label=step_label,
            artifacts=artifacts,
            result=result,
            stdout=stdout,
            stderr=stderr,
            exit_code=exit_code,
            usage=usage,
            failure=failure,
            started_at=started_at,
            finished_at=finished_at,
        )

        execution_response.additional_properties = d
        return execution_response

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
