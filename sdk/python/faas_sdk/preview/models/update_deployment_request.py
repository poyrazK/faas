from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UpdateDeploymentRequest")


@_attrs_define
class UpdateDeploymentRequest:
    """Body for PATCH /v1/deployments/{id} (issue #557 closure / ADR-072). The only mutable field post-create is the per-
    deployment cold-wake floor; image / digest / overrides / sidecars stay immutable.

    """

    min_instances: int
    """Per-deployment cold-wake floor override for PATCH /v1/deployments/{id}. 0 = inherit from parent app;
    positive value is the deployment's own floor. Effective per-instance floor = max(app, deployment). Validated
    against the parent app's plan MaxMinInstances cap."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        min_instances = self.min_instances

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "min_instances": min_instances,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        min_instances = d.pop("min_instances")

        update_deployment_request = cls(
            min_instances=min_instances,
        )

        update_deployment_request.additional_properties = d
        return update_deployment_request

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
