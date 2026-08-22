from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.env_diff_row import EnvDiffRow


T = TypeVar("T", bound="EnvDiffResponse")


@_attrs_define
class EnvDiffResponse:
    """Top-level response shape for GET /v1/apps/{slug}/env-diff
    (ADR-117 PR-C). The matrix is always full (no `?scope=`
    filter in v1). Rows are sorted ASC by key; scopes are
    sorted ASC. Bounded payload: row count <= SecretCountMax +
    EnvCountMax (200 on Scale), column count = customer's
    scope set (1-3 typical).

    """

    app_slug: str
    """Echoes the URL path parameter so the dashboard can render a header without re-parsing the URL."""
    scopes: list[str]
    """Sorted ASC list of scopes present in the matrix. Consumers iterate this list for column ordering."""
    rows: list[EnvDiffRow]
    """Sorted ASC (by key) list of env-diff rows."""
    generated_at: datetime.datetime
    """RFC3339Nano timestamp the response was built. Dashboards use this to display stale badges."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        scopes = self.scopes

        rows = []
        for rows_item_data in self.rows:
            rows_item = rows_item_data.to_dict()
            rows.append(rows_item)

        generated_at = self.generated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "scopes": scopes,
                "rows": rows,
                "generated_at": generated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.env_diff_row import EnvDiffRow

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        scopes = cast(list[str], d.pop("scopes"))

        rows = []
        _rows = d.pop("rows")
        for rows_item_data in _rows:
            rows_item = EnvDiffRow.from_dict(rows_item_data)

            rows.append(rows_item)

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        env_diff_response = cls(
            app_slug=app_slug,
            scopes=scopes,
            rows=rows,
            generated_at=generated_at,
        )

        env_diff_response.additional_properties = d
        return env_diff_response

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
