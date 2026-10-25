from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_error_summary_item import AppErrorSummaryItem


T = TypeVar("T", bound="AppErrorsSummaryResponse")


@_attrs_define
class AppErrorsSummaryResponse:
    """Grouped error summary returned by `GET /v1/apps/{slug}/errors/summary`
    (ADR-096 / PR-B). One row per fingerprint over the
    requested `[since, until]` window. Distinct from
    `AppSLOResponse` (ADR-082) which is the closed-set SLO
    summary — the errors summary uses a continuous window
    with explicit RFC3339Nano stamps.

    Empty result returns 200 with `items: []` and the
    window echo set. `window_clamped` is true when the
    requested span was clamped to `AppErrorsWindowMaxHours`
    (168h). `next_cursor` is empty when the page is the
    last one.

    """

    generated_at: datetime.datetime
    """RFC3339Nano UTC stamp at which the summary was assembled."""
    app_id: str
    app_slug: str
    window_start: datetime.datetime
    """Echoed (clamped) window start, RFC3339Nano UTC."""
    window_end: datetime.datetime
    """Echoed (clamped) window end, RFC3339Nano UTC."""
    window_clamped: bool
    """True when the requested span was clamped to AppErrorsWindowMaxHours (168h)."""
    items: list[AppErrorSummaryItem]
    limit: int
    """Echoed limit applied (post-clamp to AppErrorsSummaryMaxLimit=100)."""
    next_cursor: None | str | Unset = UNSET
    """Opaque base64 cursor for the next page. Empty when the current page is the last."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        app_id = self.app_id

        app_slug = self.app_slug

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        window_clamped = self.window_clamped

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        limit = self.limit

        next_cursor: None | str | Unset
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "app_id": app_id,
                "app_slug": app_slug,
                "window_start": window_start,
                "window_end": window_end,
                "window_clamped": window_clamped,
                "items": items,
                "limit": limit,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_error_summary_item import AppErrorSummaryItem

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        app_id = d.pop("app_id")

        app_slug = d.pop("app_slug")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        window_clamped = d.pop("window_clamped")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = AppErrorSummaryItem.from_dict(items_item_data)

            items.append(items_item)

        limit = d.pop("limit")

        def _parse_next_cursor(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        next_cursor = _parse_next_cursor(d.pop("next_cursor", UNSET))

        app_errors_summary_response = cls(
            generated_at=generated_at,
            app_id=app_id,
            app_slug=app_slug,
            window_start=window_start,
            window_end=window_end,
            window_clamped=window_clamped,
            items=items,
            limit=limit,
            next_cursor=next_cursor,
        )

        app_errors_summary_response.additional_properties = d
        return app_errors_summary_response

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
