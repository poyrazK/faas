from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_worker_health_status import ProjectWorkerHealthStatus, check_project_worker_health_status

if TYPE_CHECKING:
    from ..models.project_worker_job_sample import ProjectWorkerJobSample


T = TypeVar("T", bound="ProjectWorkerHealth")


@_attrs_define
class ProjectWorkerHealth:
    """Opt-in real production queue delivery verification; absence of traffic never proves health."""

    status: ProjectWorkerHealthStatus
    candidate: ProjectWorkerJobSample
    """Attributed delivery attempts in a bounded window, including unknown unfinished deliveries."""
    baseline: ProjectWorkerJobSample
    """Attributed delivery attempts in a bounded window, including unknown unfinished deliveries."""
    pending_jobs: int
    oldest_pending_age_ms: int
    baseline_pending_jobs: int
    baseline_oldest_pending_age_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        candidate = self.candidate.to_dict()

        baseline = self.baseline.to_dict()

        pending_jobs = self.pending_jobs

        oldest_pending_age_ms = self.oldest_pending_age_ms

        baseline_pending_jobs = self.baseline_pending_jobs

        baseline_oldest_pending_age_ms = self.baseline_oldest_pending_age_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "candidate": candidate,
                "baseline": baseline,
                "pending_jobs": pending_jobs,
                "oldest_pending_age_ms": oldest_pending_age_ms,
                "baseline_pending_jobs": baseline_pending_jobs,
                "baseline_oldest_pending_age_ms": baseline_oldest_pending_age_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_worker_job_sample import ProjectWorkerJobSample

        d = dict(src_dict)
        status = check_project_worker_health_status(d.pop("status"))

        candidate = ProjectWorkerJobSample.from_dict(d.pop("candidate"))

        baseline = ProjectWorkerJobSample.from_dict(d.pop("baseline"))

        pending_jobs = d.pop("pending_jobs")

        oldest_pending_age_ms = d.pop("oldest_pending_age_ms")

        baseline_pending_jobs = d.pop("baseline_pending_jobs")

        baseline_oldest_pending_age_ms = d.pop("baseline_oldest_pending_age_ms")

        project_worker_health = cls(
            status=status,
            candidate=candidate,
            baseline=baseline,
            pending_jobs=pending_jobs,
            oldest_pending_age_ms=oldest_pending_age_ms,
            baseline_pending_jobs=baseline_pending_jobs,
            baseline_oldest_pending_age_ms=baseline_oldest_pending_age_ms,
        )

        project_worker_health.additional_properties = d
        return project_worker_health

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
