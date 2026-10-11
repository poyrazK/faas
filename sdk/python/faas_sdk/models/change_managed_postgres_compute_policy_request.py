from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ChangeManagedPostgresComputePolicyRequest")


@_attrs_define
class ChangeManagedPostgresComputePolicyRequest:
    """Canonical UUID request for a scale-to-zero change on an existing managed PostgreSQL database."""

    request_id: UUID
    """Canonical nonzero UUID for this policy intent; replay with the same database and scale-to-zero setting after
    uncertain responses."""
    scale_to_zero: bool

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        scale_to_zero = self.scale_to_zero

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "scale_to_zero": scale_to_zero,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        scale_to_zero = d.pop("scale_to_zero")

        change_managed_postgres_compute_policy_request = cls(
            request_id=request_id,
            scale_to_zero=scale_to_zero,
        )

        return change_managed_postgres_compute_policy_request
