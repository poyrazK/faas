from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.template_view_category import TemplateViewCategory, check_template_view_category

T = TypeVar("T", bound="TemplateView")


@_attrs_define
class TemplateView:
    """A starter template entry from GET /v1/templates (issue #961 / Mega-B PR-3)."""

    name: str
    """The template name — matches cmd/gregale/templates/embed.go::Names verbatim."""
    category: TemplateViewCategory
    """Customer-facing group label (templates.CategoryFor)."""
    description: str
    """One-line customer-facing blurb."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        category: str = self.category

        description = self.description

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "category": category,
                "description": description,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        category = check_template_view_category(d.pop("category"))

        description = d.pop("description")

        template_view = cls(
            name=name,
            category=category,
            description=description,
        )

        template_view.additional_properties = d
        return template_view

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
