from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagEdgeAction")


@_attrs_define
class FlagEdgeAction:
    """What a variant does to a routed request."""

    rewrite: str
    """The path served instead: the whole path for an exact route, the prefix for a /prefix/* route."""

    def to_dict(self) -> dict[str, Any]:
        rewrite = self.rewrite

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rewrite": rewrite,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rewrite = d.pop("rewrite")

        flag_edge_action = cls(
            rewrite=rewrite,
        )

        return flag_edge_action
