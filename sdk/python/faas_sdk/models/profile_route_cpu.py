from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_route_label_coverage import ProfileRouteLabelCoverage


T = TypeVar("T", bound="ProfileRouteCPU")


@_attrs_define
class ProfileRouteCPU:
    route: str
    cpu_seconds: float
    label_coverage: ProfileRouteLabelCoverage | Unset = UNSET
    """Application-reported labeled request entries in retained whole captures divided by weighted observed
    requests in the deployment window. Boundary captures are excluded without extrapolation. This is not proof of
    full request instrumentation. Counts above observed traffic cannot be reconciled."""
    requests: int | Unset = UNSET
    cpu_seconds_per_request: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        cpu_seconds = self.cpu_seconds

        label_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.label_coverage, Unset):
            label_coverage = self.label_coverage.to_dict()

        requests = self.requests

        cpu_seconds_per_request = self.cpu_seconds_per_request

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "cpu_seconds": cpu_seconds,
            }
        )
        if label_coverage is not UNSET:
            field_dict["label_coverage"] = label_coverage
        if requests is not UNSET:
            field_dict["requests"] = requests
        if cpu_seconds_per_request is not UNSET:
            field_dict["cpu_seconds_per_request"] = cpu_seconds_per_request

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_route_label_coverage import ProfileRouteLabelCoverage

        d = dict(src_dict)
        route = d.pop("route")

        cpu_seconds = d.pop("cpu_seconds")

        _label_coverage = d.pop("label_coverage", UNSET)
        label_coverage: ProfileRouteLabelCoverage | Unset
        if isinstance(_label_coverage, Unset):
            label_coverage = UNSET
        else:
            label_coverage = ProfileRouteLabelCoverage.from_dict(_label_coverage)

        requests = d.pop("requests", UNSET)

        cpu_seconds_per_request = d.pop("cpu_seconds_per_request", UNSET)

        profile_route_cpu = cls(
            route=route,
            cpu_seconds=cpu_seconds,
            label_coverage=label_coverage,
            requests=requests,
            cpu_seconds_per_request=cpu_seconds_per_request,
        )

        profile_route_cpu.additional_properties = d
        return profile_route_cpu

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
