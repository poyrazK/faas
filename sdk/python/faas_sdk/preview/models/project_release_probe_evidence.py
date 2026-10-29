from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_release_probe import ProjectReleaseProbe


T = TypeVar("T", bound="ProjectReleaseProbeEvidence")


@_attrs_define
class ProjectReleaseProbeEvidence:
    """Attributed candidate and retained baseline samples for a critical probe round."""

    probe: ProjectReleaseProbe
    """Concrete read-only critical endpoint bound to one project workload."""
    candidate_requests: int
    candidate_errors: int
    stable_requests: int
    stable_errors: int
    unauthenticated: int
    unexpected_responses: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        probe = self.probe.to_dict()

        candidate_requests = self.candidate_requests

        candidate_errors = self.candidate_errors

        stable_requests = self.stable_requests

        stable_errors = self.stable_errors

        unauthenticated = self.unauthenticated

        unexpected_responses = self.unexpected_responses

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "probe": probe,
                "candidate_requests": candidate_requests,
                "candidate_errors": candidate_errors,
                "stable_requests": stable_requests,
                "stable_errors": stable_errors,
                "unauthenticated": unauthenticated,
                "unexpected_responses": unexpected_responses,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_probe import ProjectReleaseProbe

        d = dict(src_dict)
        probe = ProjectReleaseProbe.from_dict(d.pop("probe"))

        candidate_requests = d.pop("candidate_requests")

        candidate_errors = d.pop("candidate_errors")

        stable_requests = d.pop("stable_requests")

        stable_errors = d.pop("stable_errors")

        unauthenticated = d.pop("unauthenticated")

        unexpected_responses = d.pop("unexpected_responses")

        project_release_probe_evidence = cls(
            probe=probe,
            candidate_requests=candidate_requests,
            candidate_errors=candidate_errors,
            stable_requests=stable_requests,
            stable_errors=stable_errors,
            unauthenticated=unauthenticated,
            unexpected_responses=unexpected_responses,
        )

        project_release_probe_evidence.additional_properties = d
        return project_release_probe_evidence

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
