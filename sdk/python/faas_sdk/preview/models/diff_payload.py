from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.diff_payload_plan import DiffPayloadPlan, check_diff_payload_plan
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.diff_break import DiffBreak
    from ..models.diff_change import DiffChange


T = TypeVar("T", bound="DiffPayload")


@_attrs_define
class DiffPayload:
    """Inner diff object the engine produces. Slug + Plan +
    Changes + Breaks. Wrapped by [DiffResponse] so a CI
    consumer reading `.diff.changes` and a CLI consumer
    reading the top-level keys agree.

    """

    slug: str
    changes: list[DiffChange]
    breaks: list[DiffBreak]
    plan: DiffPayloadPlan | Unset = UNSET
    """Customer's subscription tier (echoed from acct.Plan)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        breaks = []
        for breaks_item_data in self.breaks:
            breaks_item = breaks_item_data.to_dict()
            breaks.append(breaks_item)

        plan: str | Unset = UNSET
        if not isinstance(self.plan, Unset):
            plan = self.plan

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "changes": changes,
                "breaks": breaks,
            }
        )
        if plan is not UNSET:
            field_dict["plan"] = plan

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.diff_break import DiffBreak
        from ..models.diff_change import DiffChange

        d = dict(src_dict)
        slug = d.pop("slug")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = DiffChange.from_dict(changes_item_data)

            changes.append(changes_item)

        breaks = []
        _breaks = d.pop("breaks")
        for breaks_item_data in _breaks:
            breaks_item = DiffBreak.from_dict(breaks_item_data)

            breaks.append(breaks_item)

        _plan = d.pop("plan", UNSET)
        plan: DiffPayloadPlan | Unset
        if isinstance(_plan, Unset):
            plan = UNSET
        else:
            plan = check_diff_payload_plan(_plan)

        diff_payload = cls(
            slug=slug,
            changes=changes,
            breaks=breaks,
            plan=plan,
        )

        diff_payload.additional_properties = d
        return diff_payload

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
