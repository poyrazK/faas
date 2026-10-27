from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PrepareProjectRollbackRequest")


@_attrs_define
class PrepareProjectRollbackRequest:
    """Historical release selection used to prepare a checked project rollback."""

    release_id: UUID
    """Retained inactive historical graph in this project environment."""
    ttl_seconds: int

    def to_dict(self) -> dict[str, Any]:
        release_id = str(self.release_id)

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_id": release_id,
                "ttl_seconds": ttl_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        release_id = UUID(d.pop("release_id"))

        ttl_seconds = d.pop("ttl_seconds")

        prepare_project_rollback_request = cls(
            release_id=release_id,
            ttl_seconds=ttl_seconds,
        )

        return prepare_project_rollback_request
