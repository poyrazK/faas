from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.flag_edge_route_variants import FlagEdgeRouteVariants


T = TypeVar("T", bound="FlagEdgeRoute")


@_attrs_define
class FlagEdgeRoute:
    """One routed path: /page matches exactly, /prefix/* matches the prefix and everything under it. Variants without an
    action serve the request unchanged.

    """

    path: str
    variants: FlagEdgeRouteVariants
    """Variant key to the action requests decided as that variant get."""

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        variants = self.variants.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
                "variants": variants,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_edge_route_variants import FlagEdgeRouteVariants

        d = dict(src_dict)
        path = d.pop("path")

        variants = FlagEdgeRouteVariants.from_dict(d.pop("variants"))

        flag_edge_route = cls(
            path=path,
            variants=variants,
        )

        return flag_edge_route
