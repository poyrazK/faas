from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="HostedTaskBindingCheck")


@_attrs_define
class HostedTaskBindingCheck:
    """Exact immutable qualification fingerprint supplied by a personal owner for a check or revocation."""

    binding_sha256: str

    def to_dict(self) -> dict[str, Any]:
        binding_sha256 = self.binding_sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "binding_sha256": binding_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        binding_sha256 = d.pop("binding_sha256")

        hosted_task_binding_check = cls(
            binding_sha256=binding_sha256,
        )

        return hosted_task_binding_check
