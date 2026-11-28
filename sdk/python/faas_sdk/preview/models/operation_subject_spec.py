from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationSubjectSpec")


@_attrs_define
class OperationSubjectSpec:
    """Optional public business reference extracted once from validated input on new admission. This metadata does not
    authorize access to the business entity.

    """

    type_: str
    id_from: str
    """JSON Pointer into input. The selected value must be a nonempty string; array indices use canonical decimal
    notation. Maximum 2048 UTF-8 bytes; ASCII controls are rejected."""

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        id_from = self.id_from

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "type": type_,
                "id_from": id_from,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = d.pop("type")

        id_from = d.pop("id_from")

        operation_subject_spec = cls(
            type_=type_,
            id_from=id_from,
        )

        return operation_subject_spec
