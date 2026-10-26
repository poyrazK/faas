from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.seat_usage_response_plan import SeatUsageResponsePlan, check_seat_usage_response_plan

T = TypeVar("T", bound="SeatUsageResponse")


@_attrs_define
class SeatUsageResponse:
    """GET /v1/orgs/{slug}/seat_usage response. Visibility-only —
    PR 9 ships the per-seat pricing cut-over per ADR-061
    §"Out of scope". `used` counts active members only (the
    store's `CountActiveOrgMembers` filters `removed_at IS
    NULL`). `limit` comes from `org.Plan.OrgMembersMax()` —
    the `free` plan returns `0` so the dashboard can render
    "personal org only" instead of "0 of 0 used".

    """

    used: int
    """Active member count."""
    limit: int
    """Plan cap on active members (`Plan.OrgMembersMax()`).
    Returns `0` for the `free` plan — the fail-closed
    accessor shape.
    """
    plan: SeatUsageResponsePlan
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        used = self.used

        limit = self.limit

        plan: str = self.plan

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "used": used,
                "limit": limit,
                "plan": plan,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        used = d.pop("used")

        limit = d.pop("limit")

        plan = check_seat_usage_response_plan(d.pop("plan"))

        seat_usage_response = cls(
            used=used,
            limit=limit,
            plan=plan,
        )

        seat_usage_response.additional_properties = d
        return seat_usage_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
