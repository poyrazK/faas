from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagRolloutPromotionRequest")


@_attrs_define
class FlagRolloutPromotionRequest:
    """Optimistic stage promotion request; an expected-version conflict requires a fresh read."""

    expected_version: int
    rule_id: str

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        rule_id = self.rule_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "rule_id": rule_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        rule_id = d.pop("rule_id")

        flag_rollout_promotion_request = cls(
            expected_version=expected_version,
            rule_id=rule_id,
        )

        return flag_rollout_promotion_request
