from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.flag_condition_op import FlagConditionOp, check_flag_condition_op

T = TypeVar("T", bound="FlagCondition")


@_attrs_define
class FlagCondition:
    """One request attribute condition. gregale.country (ISO 3166-1 alpha-2) and gregale.ua_family are verified by the
    gateway; app.<name> attributes are supplied by the application after it authenticates the user. A missing attribute
    never matches, including not_in. ends_with applies only to app.* attributes.

    """

    attribute: str
    op: FlagConditionOp
    values: list[str]

    def to_dict(self) -> dict[str, Any]:
        attribute = self.attribute

        op: str = self.op

        values = self.values

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "attribute": attribute,
                "op": op,
                "values": values,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attribute = d.pop("attribute")

        op = check_flag_condition_op(d.pop("op"))

        values = cast(list[str], d.pop("values"))

        flag_condition = cls(
            attribute=attribute,
            op=op,
            values=values,
        )

        return flag_condition
