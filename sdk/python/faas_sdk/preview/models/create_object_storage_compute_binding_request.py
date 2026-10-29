from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_object_storage_compute_binding_request_permission import (
    CreateObjectStorageComputeBindingRequestPermission,
    check_create_object_storage_compute_binding_request_permission,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateObjectStorageComputeBindingRequest")


@_attrs_define
class CreateObjectStorageComputeBindingRequest:
    """Least-privilege S3 access and the environment-variable prefix for an app-to-bucket binding."""

    permission: CreateObjectStorageComputeBindingRequestPermission
    label: str | Unset = "compute"
    prefix: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        permission: str = self.permission

        label = self.label

        prefix = self.prefix

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "permission": permission,
            }
        )
        if label is not UNSET:
            field_dict["label"] = label
        if prefix is not UNSET:
            field_dict["prefix"] = prefix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        permission = check_create_object_storage_compute_binding_request_permission(d.pop("permission"))

        label = d.pop("label", UNSET)

        prefix = d.pop("prefix", UNSET)

        create_object_storage_compute_binding_request = cls(
            permission=permission,
            label=label,
            prefix=prefix,
        )

        return create_object_storage_compute_binding_request
