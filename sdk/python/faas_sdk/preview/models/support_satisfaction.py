from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SupportSatisfaction")


@_attrs_define
class SupportSatisfaction:
    """Current resolution survey eligibility and any previously submitted score."""

    episode_id: UUID
    eligible: bool
    rating: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        episode_id = str(self.episode_id)

        eligible = self.eligible

        rating = self.rating

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "episode_id": episode_id,
                "eligible": eligible,
            }
        )
        if rating is not UNSET:
            field_dict["rating"] = rating

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        episode_id = UUID(d.pop("episode_id"))

        eligible = d.pop("eligible")

        rating = d.pop("rating", UNSET)

        support_satisfaction = cls(
            episode_id=episode_id,
            eligible=eligible,
            rating=rating,
        )

        return support_satisfaction
