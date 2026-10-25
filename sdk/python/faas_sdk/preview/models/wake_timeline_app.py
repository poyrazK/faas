from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WakeTimelineApp")


@_attrs_define
class WakeTimelineApp:
    """Slim per-app identification embedded in `AppWakeTimelineResponse`.
    Carries only the fields the dashboard SPA needs for the
    wake-timeline header (slug + app_id). The wider
    pkg/dashboard.AppListItem type carries template-specific
    glyph/badge fields (SLO badge, StateBadge*, QuotaLabel) that
    don't belong on the wire.

    """

    app_id: UUID
    slug: str
    """DNS-safe app slug (matches apps.slug)."""
    status: str | Unset = UNSET
    """Optional deployment status (active/paused). Empty until a deployment is bound."""
    url: str | Unset = UNSET
    """Optional public URL once a deployment is bound."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        slug = self.slug

        status = self.status

        url = self.url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
            }
        )
        if status is not UNSET:
            field_dict["status"] = status
        if url is not UNSET:
            field_dict["url"] = url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        slug = d.pop("slug")

        status = d.pop("status", UNSET)

        url = d.pop("url", UNSET)

        wake_timeline_app = cls(
            app_id=app_id,
            slug=slug,
            status=status,
            url=url,
        )

        wake_timeline_app.additional_properties = d
        return wake_timeline_app

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
