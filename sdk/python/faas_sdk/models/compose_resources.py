from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.compose_resources_cpu_millicores import (
    ComposeResourcesCpuMillicores,
    check_compose_resources_cpu_millicores,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ComposeResources")


@_attrs_define
class ComposeResources:
    """A Compose service's deploy.replicas and deploy.resources.limits, converted to app settings. Omitted fields are
    undeclared and keep the app's own settings.

    """

    replicas: int | Unset = UNSET
    """Caps a request-mode app's instances (max_concurrency) and fixes a worker's or service's replica count."""
    memory_mb: int | Unset = UNSET
    """App RAM in MB, rounded up from the declared size."""
    cpu_millicores: ComposeResourcesCpuMillicores | Unset = UNSET
    """App CPU share."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        replicas = self.replicas

        memory_mb = self.memory_mb

        cpu_millicores: int | Unset = UNSET
        if not isinstance(self.cpu_millicores, Unset):
            cpu_millicores = self.cpu_millicores

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if replicas is not UNSET:
            field_dict["replicas"] = replicas
        if memory_mb is not UNSET:
            field_dict["memory_mb"] = memory_mb
        if cpu_millicores is not UNSET:
            field_dict["cpu_millicores"] = cpu_millicores

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        replicas = d.pop("replicas", UNSET)

        memory_mb = d.pop("memory_mb", UNSET)

        _cpu_millicores = d.pop("cpu_millicores", UNSET)
        cpu_millicores: ComposeResourcesCpuMillicores | Unset
        if isinstance(_cpu_millicores, Unset):
            cpu_millicores = UNSET
        else:
            cpu_millicores = check_compose_resources_cpu_millicores(_cpu_millicores)

        compose_resources = cls(
            replicas=replicas,
            memory_mb=memory_mb,
            cpu_millicores=cpu_millicores,
        )

        compose_resources.additional_properties = d
        return compose_resources

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
