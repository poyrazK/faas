from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionFileInput")


@_attrs_define
class ExecutionFileInput:
    """Exactly one file_id or single-use artifact grant_token. Data is staged in the fresh guest input directory and is
    excluded from the source bundle budget.

    """

    path: str
    file_id: UUID | Unset = UNSET
    grant_token: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        file_id: str | Unset = UNSET
        if not isinstance(self.file_id, Unset):
            file_id = str(self.file_id)

        grant_token = self.grant_token

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
            }
        )
        if file_id is not UNSET:
            field_dict["file_id"] = file_id
        if grant_token is not UNSET:
            field_dict["grant_token"] = grant_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        _file_id = d.pop("file_id", UNSET)
        file_id: UUID | Unset
        if isinstance(_file_id, Unset):
            file_id = UNSET
        else:
            file_id = UUID(_file_id)

        grant_token = d.pop("grant_token", UNSET)

        execution_file_input = cls(
            path=path,
            file_id=file_id,
            grant_token=grant_token,
        )

        return execution_file_input
