from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.diff_response_plan import DiffResponsePlan, check_diff_response_plan
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.diff_payload import DiffPayload


T = TypeVar("T", bound="DiffResponse")


@_attrs_define
class DiffResponse:
    """Wire envelope for POST /v1/apps/{slug}/diff and
    `gregale deploy --diff --json`. Wraps the [DiffPayload]
    plus the Blocking bit so a CI consumer doesn't have to
    re-scan Breaks and pick the max severity.

    """

    diff: DiffPayload
    """Inner diff object the engine produces. Slug + Plan +
    Changes + Breaks. Wrapped by [DiffResponse] so a CI
    consumer reading `.diff.changes` and a CLI consumer
    reading the top-level keys agree.
    """
    blocking: bool
    """True if any break has severity "error". Mirrors
    [pkg/deploydiff.Diff.HasBlockingBreaks]. The exit-1
    input for CI gates.
    """
    slug: str
    plan: DiffResponsePlan | Unset = UNSET
    """Echoed at top level too — kept in sync with diff.plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        diff = self.diff.to_dict()

        blocking = self.blocking

        slug = self.slug

        plan: str | Unset = UNSET
        if not isinstance(self.plan, Unset):
            plan = self.plan

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "diff": diff,
                "blocking": blocking,
                "slug": slug,
            }
        )
        if plan is not UNSET:
            field_dict["plan"] = plan

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.diff_payload import DiffPayload

        d = dict(src_dict)
        diff = DiffPayload.from_dict(d.pop("diff"))

        blocking = d.pop("blocking")

        slug = d.pop("slug")

        _plan = d.pop("plan", UNSET)
        plan: DiffResponsePlan | Unset
        if isinstance(_plan, Unset):
            plan = UNSET
        else:
            plan = check_diff_response_plan(_plan)

        diff_response = cls(
            diff=diff,
            blocking=blocking,
            slug=slug,
            plan=plan,
        )

        diff_response.additional_properties = d
        return diff_response

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
