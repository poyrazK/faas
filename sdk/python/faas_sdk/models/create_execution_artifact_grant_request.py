from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateExecutionArtifactGrantRequest")


@_attrs_define
class CreateExecutionArtifactGrantRequest:
    """Request to create a short-lived, one-time capability for one output artifact."""

    artifact_name: str
    """Exact output artifact to grant."""
    expires_in_seconds: int | Unset = 300
    """Grant lifetime. Defaults to five minutes."""

    def to_dict(self) -> dict[str, Any]:
        artifact_name = self.artifact_name

        expires_in_seconds = self.expires_in_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "artifact_name": artifact_name,
            }
        )
        if expires_in_seconds is not UNSET:
            field_dict["expires_in_seconds"] = expires_in_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        artifact_name = d.pop("artifact_name")

        expires_in_seconds = d.pop("expires_in_seconds", UNSET)

        create_execution_artifact_grant_request = cls(
            artifact_name=artifact_name,
            expires_in_seconds=expires_in_seconds,
        )

        return create_execution_artifact_grant_request
