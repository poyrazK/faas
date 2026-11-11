from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplyAppScalingRequest")


@_attrs_define
class ApplyAppScalingRequest:
    """A reviewed partial scaling change with a durable request identity."""

    expected_app_id: UUID
    """Immutable app UUID from the reviewed scaling snapshot."""
    request_id: UUID
    expected_revision: int
    expected_environment_id: UUID | Unset = UNSET
    """Required for a named environment; must match its immutable identity."""
    ram_mb: int | Unset = UNSET
    cpu_millicores: int | Unset = UNSET
    max_concurrency: int | Unset = UNSET
    min_instances: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_app_id = str(self.expected_app_id)

        request_id = str(self.request_id)

        expected_revision = self.expected_revision

        expected_environment_id: str | Unset = UNSET
        if not isinstance(self.expected_environment_id, Unset):
            expected_environment_id = str(self.expected_environment_id)

        ram_mb = self.ram_mb

        cpu_millicores = self.cpu_millicores

        max_concurrency = self.max_concurrency

        min_instances = self.min_instances

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_app_id": expected_app_id,
                "request_id": request_id,
                "expected_revision": expected_revision,
            }
        )
        if expected_environment_id is not UNSET:
            field_dict["expected_environment_id"] = expected_environment_id
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if cpu_millicores is not UNSET:
            field_dict["cpu_millicores"] = cpu_millicores
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if min_instances is not UNSET:
            field_dict["min_instances"] = min_instances

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_app_id = UUID(d.pop("expected_app_id"))

        request_id = UUID(d.pop("request_id"))

        expected_revision = d.pop("expected_revision")

        _expected_environment_id = d.pop("expected_environment_id", UNSET)
        expected_environment_id: UUID | Unset
        if isinstance(_expected_environment_id, Unset):
            expected_environment_id = UNSET
        else:
            expected_environment_id = UUID(_expected_environment_id)

        ram_mb = d.pop("ram_mb", UNSET)

        cpu_millicores = d.pop("cpu_millicores", UNSET)

        max_concurrency = d.pop("max_concurrency", UNSET)

        min_instances = d.pop("min_instances", UNSET)

        apply_app_scaling_request = cls(
            expected_app_id=expected_app_id,
            request_id=request_id,
            expected_revision=expected_revision,
            expected_environment_id=expected_environment_id,
            ram_mb=ram_mb,
            cpu_millicores=cpu_millicores,
            max_concurrency=max_concurrency,
            min_instances=min_instances,
        )

        return apply_app_scaling_request
