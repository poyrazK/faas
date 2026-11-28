from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionArtifactInput")


@_attrs_define
class ExecutionArtifactInput:
    """One same-family artifact reference or a one-time cross-agent grant, staged at a normalized path in the next run's
    ephemeral bundle.

    """

    path: str
    """Normalized relative destination path in the new execution bundle."""
    execution_id: UUID | Unset = UNSET
    """ID of a successful execution owned by this key family (omit when using grant_token)."""
    name: str | Unset = UNSET
    """Exact artifact name from the source execution receipt (omit when using grant_token)."""
    grant_token: str | Unset = UNSET
    """Single-use token created for one artifact by another Runs key family."""

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        execution_id: str | Unset = UNSET
        if not isinstance(self.execution_id, Unset):
            execution_id = str(self.execution_id)

        name = self.name

        grant_token = self.grant_token

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
            }
        )
        if execution_id is not UNSET:
            field_dict["execution_id"] = execution_id
        if name is not UNSET:
            field_dict["name"] = name
        if grant_token is not UNSET:
            field_dict["grant_token"] = grant_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        _execution_id = d.pop("execution_id", UNSET)
        execution_id: UUID | Unset
        if isinstance(_execution_id, Unset):
            execution_id = UNSET
        else:
            execution_id = UUID(_execution_id)

        name = d.pop("name", UNSET)

        grant_token = d.pop("grant_token", UNSET)

        execution_artifact_input = cls(
            path=path,
            execution_id=execution_id,
            name=name,
            grant_token=grant_token,
        )

        return execution_artifact_input
