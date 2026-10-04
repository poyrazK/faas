from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="GlobalRuntimeQuotaTargetResponse")


@_attrs_define
class GlobalRuntimeQuotaTargetResponse:
    """Original regional identity, reserved capacity and monotone issuance/release accounting."""

    cell_id: UUID
    region: str
    cell_generation: int
    regional_account_id: UUID
    regional_app_id: UUID
    regional_app_slug: str
    concurrency: int
    rollout_concurrency: int
    issued_at: datetime.datetime | None
    """Durable directory issuance marker; does not prove delivery or scheduler acceptance."""
    released_at: datetime.datetime | None
    """Unissued retirement or internally verified regional drain release."""
    regional_org_id: UUID | Unset = UNSET
    """Snapshotted organization tenant; omitted for personal apps."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cell_id = str(self.cell_id)

        region = self.region

        cell_generation = self.cell_generation

        regional_account_id = str(self.regional_account_id)

        regional_app_id = str(self.regional_app_id)

        regional_app_slug = self.regional_app_slug

        concurrency = self.concurrency

        rollout_concurrency = self.rollout_concurrency

        issued_at: None | str
        if isinstance(self.issued_at, datetime.datetime):
            issued_at = self.issued_at.isoformat()
        else:
            issued_at = self.issued_at

        released_at: None | str
        if isinstance(self.released_at, datetime.datetime):
            released_at = self.released_at.isoformat()
        else:
            released_at = self.released_at

        regional_org_id: str | Unset = UNSET
        if not isinstance(self.regional_org_id, Unset):
            regional_org_id = str(self.regional_org_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cell_id": cell_id,
                "region": region,
                "cell_generation": cell_generation,
                "regional_account_id": regional_account_id,
                "regional_app_id": regional_app_id,
                "regional_app_slug": regional_app_slug,
                "concurrency": concurrency,
                "rollout_concurrency": rollout_concurrency,
                "issued_at": issued_at,
                "released_at": released_at,
            }
        )
        if regional_org_id is not UNSET:
            field_dict["regional_org_id"] = regional_org_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cell_id = UUID(d.pop("cell_id"))

        region = d.pop("region")

        cell_generation = d.pop("cell_generation")

        regional_account_id = UUID(d.pop("regional_account_id"))

        regional_app_id = UUID(d.pop("regional_app_id"))

        regional_app_slug = d.pop("regional_app_slug")

        concurrency = d.pop("concurrency")

        rollout_concurrency = d.pop("rollout_concurrency")

        def _parse_issued_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                issued_at_type_0 = datetime.datetime.fromisoformat(data)

                return issued_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        issued_at = _parse_issued_at(d.pop("issued_at"))

        def _parse_released_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                released_at_type_0 = datetime.datetime.fromisoformat(data)

                return released_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        released_at = _parse_released_at(d.pop("released_at"))

        _regional_org_id = d.pop("regional_org_id", UNSET)
        regional_org_id: UUID | Unset
        if isinstance(_regional_org_id, Unset):
            regional_org_id = UNSET
        else:
            regional_org_id = UUID(_regional_org_id)

        global_runtime_quota_target_response = cls(
            cell_id=cell_id,
            region=region,
            cell_generation=cell_generation,
            regional_account_id=regional_account_id,
            regional_app_id=regional_app_id,
            regional_app_slug=regional_app_slug,
            concurrency=concurrency,
            rollout_concurrency=rollout_concurrency,
            issued_at=issued_at,
            released_at=released_at,
            regional_org_id=regional_org_id,
        )

        global_runtime_quota_target_response.additional_properties = d
        return global_runtime_quota_target_response

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
