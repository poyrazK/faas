from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppReleaseReview")


@_attrs_define
class AppReleaseReview:
    """Atomic app configuration and serving-deployment identities used to fence release admission."""

    app_id: str
    configuration_hash: str
    latest_deployment_id: str
    serving_deployment_ids: list[str]
    scope: str | Unset = UNSET
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    workload_revision: int | Unset = UNSET
    workload_config_hash: str | Unset = UNSET
    protected: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        configuration_hash = self.configuration_hash

        latest_deployment_id = self.latest_deployment_id

        serving_deployment_ids = self.serving_deployment_ids

        scope = self.scope

        project_id = self.project_id

        environment_id = self.environment_id

        workload_revision = self.workload_revision

        workload_config_hash = self.workload_config_hash

        protected = self.protected

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "configuration_hash": configuration_hash,
                "latest_deployment_id": latest_deployment_id,
                "serving_deployment_ids": serving_deployment_ids,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if workload_revision is not UNSET:
            field_dict["workload_revision"] = workload_revision
        if workload_config_hash is not UNSET:
            field_dict["workload_config_hash"] = workload_config_hash
        if protected is not UNSET:
            field_dict["protected"] = protected

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        configuration_hash = d.pop("configuration_hash")

        latest_deployment_id = d.pop("latest_deployment_id")

        serving_deployment_ids = cast(list[str], d.pop("serving_deployment_ids"))

        scope = d.pop("scope", UNSET)

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        workload_revision = d.pop("workload_revision", UNSET)

        workload_config_hash = d.pop("workload_config_hash", UNSET)

        protected = d.pop("protected", UNSET)

        app_release_review = cls(
            app_id=app_id,
            configuration_hash=configuration_hash,
            latest_deployment_id=latest_deployment_id,
            serving_deployment_ids=serving_deployment_ids,
            scope=scope,
            project_id=project_id,
            environment_id=environment_id,
            workload_revision=workload_revision,
            workload_config_hash=workload_config_hash,
            protected=protected,
        )

        app_release_review.additional_properties = d
        return app_release_review

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
