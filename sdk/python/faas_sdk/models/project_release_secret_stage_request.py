from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.project_release_secret_stage_request_workloads import ProjectReleaseSecretStageRequestWorkloads


T = TypeVar("T", bound="ProjectReleaseSecretStageRequest")


@_attrs_define
class ProjectReleaseSecretStageRequest:
    """A bounded secret staging identity and complete private workload value sets."""

    request_id: str
    workloads: ProjectReleaseSecretStageRequestWorkloads
    """Complete proposed workload secret snapshots within shared document and per-plan value limits. Values are
    accepted over TLS and sealed before storage."""

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        workloads = self.workloads.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "workloads": workloads,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_secret_stage_request_workloads import ProjectReleaseSecretStageRequestWorkloads

        d = dict(src_dict)
        request_id = d.pop("request_id")

        workloads = ProjectReleaseSecretStageRequestWorkloads.from_dict(d.pop("workloads"))

        project_release_secret_stage_request = cls(
            request_id=request_id,
            workloads=workloads,
        )

        return project_release_secret_stage_request
