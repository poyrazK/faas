from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagExperimentShipRequest")


@_attrs_define
class FlagExperimentShipRequest:
    """The variant to ship."""

    variant: str
    """A variant of the experiment's flag."""

    def to_dict(self) -> dict[str, Any]:
        variant = self.variant

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variant": variant,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant = d.pop("variant")

        flag_experiment_ship_request = cls(
            variant=variant,
        )

        return flag_experiment_ship_request
