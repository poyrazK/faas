from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationSubmissionScope")


@_attrs_define
class OperationSubmissionScope:
    """Optional immutable-definition fence for a browser feature."""

    app_id: UUID
    scope: str
    name: str

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        name = self.name

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "name": name,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        name = d.pop("name")

        operation_submission_scope = cls(
            app_id=app_id,
            scope=scope,
            name=name,
        )

        return operation_submission_scope
