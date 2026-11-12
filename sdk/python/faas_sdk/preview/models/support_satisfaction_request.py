from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SupportSatisfactionRequest")


@_attrs_define
class SupportSatisfactionRequest:
    """One satisfaction rating for the currently resolved episode."""

    episode_id: UUID
    rating: int

    def to_dict(self) -> dict[str, Any]:
        episode_id = str(self.episode_id)

        rating = self.rating

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "episode_id": episode_id,
                "rating": rating,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        episode_id = UUID(d.pop("episode_id"))

        rating = d.pop("rating")

        support_satisfaction_request = cls(
            episode_id=episode_id,
            rating=rating,
        )

        return support_satisfaction_request
