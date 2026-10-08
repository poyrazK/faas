from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteMonitorDeploymentBaseline")


@_attrs_define
class RouteMonitorDeploymentBaseline:
    """The last different fully serving deployment with a healthy route-monitor report before this incident opened.
    Provenance values are sanitized declared metadata and do not attest to deployment archive bytes.

    """

    deployment_id: UUID
    commit_sha: str | Unset = UNSET
    repository: str | Unset = UNSET
    source_root: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        commit_sha = self.commit_sha

        repository = self.repository

        source_root = self.source_root

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if repository is not UNSET:
            field_dict["repository"] = repository
        if source_root is not UNSET:
            field_dict["source_root"] = source_root

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        commit_sha = d.pop("commit_sha", UNSET)

        repository = d.pop("repository", UNSET)

        source_root = d.pop("source_root", UNSET)

        route_monitor_deployment_baseline = cls(
            deployment_id=deployment_id,
            commit_sha=commit_sha,
            repository=repository,
            source_root=source_root,
        )

        route_monitor_deployment_baseline.additional_properties = d
        return route_monitor_deployment_baseline

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
