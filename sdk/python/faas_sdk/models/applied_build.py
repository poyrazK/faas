from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppliedBuild")


@_attrs_define
class AppliedBuild:
    """Per-workload build result from the apply-time build-enqueue loop.
    On success: slug + app_id + deployment_id + build_id. On failure:
    slug + app_id + error, no IDs.

    """

    slug: str
    app_id: str
    deployment_id: str | Unset = UNSET
    build_id: str | Unset = UNSET
    deployment_status: str | Unset = UNSET
    """Terminal deployment status added by wait-aware CLI responses."""
    build_status: str | Unset = UNSET
    """Terminal build status added by wait-aware CLI responses."""
    error: str | Unset = UNSET
    """Staging or enqueue error message; partial-failure rows carry this in lieu of IDs."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        app_id = self.app_id

        deployment_id = self.deployment_id

        build_id = self.build_id

        deployment_status = self.deployment_status

        build_status = self.build_status

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "app_id": app_id,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if build_id is not UNSET:
            field_dict["build_id"] = build_id
        if deployment_status is not UNSET:
            field_dict["deployment_status"] = deployment_status
        if build_status is not UNSET:
            field_dict["build_status"] = build_status
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id", UNSET)

        build_id = d.pop("build_id", UNSET)

        deployment_status = d.pop("deployment_status", UNSET)

        build_status = d.pop("build_status", UNSET)

        error = d.pop("error", UNSET)

        applied_build = cls(
            slug=slug,
            app_id=app_id,
            deployment_id=deployment_id,
            build_id=build_id,
            deployment_status=deployment_status,
            build_status=build_status,
            error=error,
        )

        applied_build.additional_properties = d
        return applied_build

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
