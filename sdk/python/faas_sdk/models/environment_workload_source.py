from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.environment_workload_source_kind import (
    EnvironmentWorkloadSourceKind,
    check_environment_workload_source_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentWorkloadSource")


@_attrs_define
class EnvironmentWorkloadSource:
    """Build source within the approved Git tree or an immutable OCI digest."""

    kind: EnvironmentWorkloadSourceKind
    directory: str | Unset = UNSET
    dockerfile: str | Unset = UNSET
    image: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        directory = self.directory

        dockerfile = self.dockerfile

        image = self.image

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
            }
        )
        if directory is not UNSET:
            field_dict["directory"] = directory
        if dockerfile is not UNSET:
            field_dict["dockerfile"] = dockerfile
        if image is not UNSET:
            field_dict["image"] = image

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_environment_workload_source_kind(d.pop("kind"))

        directory = d.pop("directory", UNSET)

        dockerfile = d.pop("dockerfile", UNSET)

        image = d.pop("image", UNSET)

        environment_workload_source = cls(
            kind=kind,
            directory=directory,
            dockerfile=dockerfile,
            image=image,
        )

        return environment_workload_source
