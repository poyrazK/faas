from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RegionalRuntimeQuotaBinding")


@_attrs_define
class RegionalRuntimeQuotaBinding:
    """Exact original quota wire binding. Capitalized field spelling and an empty RegionalOrgID are retained for canonical
    transport compatibility.

    """

    cell_id: str
    region: str
    regional_account_id: str
    regional_org_id: str
    regional_app_id: str
    regional_app_slug: str
    api_base: str
    cell_generation: int

    def to_dict(self) -> dict[str, Any]:
        cell_id = self.cell_id

        region = self.region

        regional_account_id = self.regional_account_id

        regional_org_id = self.regional_org_id

        regional_app_id = self.regional_app_id

        regional_app_slug = self.regional_app_slug

        api_base = self.api_base

        cell_generation = self.cell_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "CellID": cell_id,
                "Region": region,
                "RegionalAccountID": regional_account_id,
                "RegionalOrgID": regional_org_id,
                "RegionalAppID": regional_app_id,
                "RegionalAppSlug": regional_app_slug,
                "APIBase": api_base,
                "CellGeneration": cell_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cell_id = d.pop("CellID")

        region = d.pop("Region")

        regional_account_id = d.pop("RegionalAccountID")

        regional_org_id = d.pop("RegionalOrgID")

        regional_app_id = d.pop("RegionalAppID")

        regional_app_slug = d.pop("RegionalAppSlug")

        api_base = d.pop("APIBase")

        cell_generation = d.pop("CellGeneration")

        regional_runtime_quota_binding = cls(
            cell_id=cell_id,
            region=region,
            regional_account_id=regional_account_id,
            regional_org_id=regional_org_id,
            regional_app_id=regional_app_id,
            regional_app_slug=regional_app_slug,
            api_base=api_base,
            cell_generation=cell_generation,
        )

        return regional_runtime_quota_binding
