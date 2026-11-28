from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_workload_progress_activation import (
    EnvironmentWorkloadProgressActivation,
    check_environment_workload_progress_activation,
)
from ..models.environment_workload_progress_build_cache_status import (
    EnvironmentWorkloadProgressBuildCacheStatus,
    check_environment_workload_progress_build_cache_status,
)
from ..models.environment_workload_progress_preparation import (
    EnvironmentWorkloadProgressPreparation,
    check_environment_workload_progress_preparation,
)
from ..models.environment_workload_progress_qualification import (
    EnvironmentWorkloadProgressQualification,
    check_environment_workload_progress_qualification,
)
from ..models.environment_workload_progress_serving import (
    EnvironmentWorkloadProgressServing,
    check_environment_workload_progress_serving,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentWorkloadProgress")


@_attrs_define
class EnvironmentWorkloadProgress:
    """Read-only progress for one exact reviewed graph member. Serving remains unknown without current whole-graph
    convergence proof.

    """

    resource: str
    app_id: str
    execution_mode: str
    preparation: EnvironmentWorkloadProgressPreparation
    qualification: EnvironmentWorkloadProgressQualification
    activation: EnvironmentWorkloadProgressActivation
    serving: EnvironmentWorkloadProgressServing
    blocking_reasons: list[str]
    deployment_id: str | Unset = UNSET
    job_id: str | Unset = UNSET
    build_status: str | Unset = UNSET
    build_cache_status: EnvironmentWorkloadProgressBuildCacheStatus | Unset = UNSET
    """Recorded builder cache decision; does not grant qualification, activation or serving authority."""
    build_cache_key_sha256: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        app_id = self.app_id

        execution_mode = self.execution_mode

        preparation: str = self.preparation

        qualification: str = self.qualification

        activation: str = self.activation

        serving: str = self.serving

        blocking_reasons = self.blocking_reasons

        deployment_id = self.deployment_id

        job_id = self.job_id

        build_status = self.build_status

        build_cache_status: str | Unset = UNSET
        if not isinstance(self.build_cache_status, Unset):
            build_cache_status = self.build_cache_status

        build_cache_key_sha256 = self.build_cache_key_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource": resource,
                "app_id": app_id,
                "execution_mode": execution_mode,
                "preparation": preparation,
                "qualification": qualification,
                "activation": activation,
                "serving": serving,
                "blocking_reasons": blocking_reasons,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if build_status is not UNSET:
            field_dict["build_status"] = build_status
        if build_cache_status is not UNSET:
            field_dict["build_cache_status"] = build_cache_status
        if build_cache_key_sha256 is not UNSET:
            field_dict["build_cache_key_sha256"] = build_cache_key_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        app_id = d.pop("app_id")

        execution_mode = d.pop("execution_mode")

        preparation = check_environment_workload_progress_preparation(d.pop("preparation"))

        qualification = check_environment_workload_progress_qualification(d.pop("qualification"))

        activation = check_environment_workload_progress_activation(d.pop("activation"))

        serving = check_environment_workload_progress_serving(d.pop("serving"))

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        deployment_id = d.pop("deployment_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        build_status = d.pop("build_status", UNSET)

        _build_cache_status = d.pop("build_cache_status", UNSET)
        build_cache_status: EnvironmentWorkloadProgressBuildCacheStatus | Unset
        if isinstance(_build_cache_status, Unset):
            build_cache_status = UNSET
        else:
            build_cache_status = check_environment_workload_progress_build_cache_status(_build_cache_status)

        build_cache_key_sha256 = d.pop("build_cache_key_sha256", UNSET)

        environment_workload_progress = cls(
            resource=resource,
            app_id=app_id,
            execution_mode=execution_mode,
            preparation=preparation,
            qualification=qualification,
            activation=activation,
            serving=serving,
            blocking_reasons=blocking_reasons,
            deployment_id=deployment_id,
            job_id=job_id,
            build_status=build_status,
            build_cache_status=build_cache_status,
            build_cache_key_sha256=build_cache_key_sha256,
        )

        environment_workload_progress.additional_properties = d
        return environment_workload_progress

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
