from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.exclusive_trigger_binding_record_source import (
    ExclusiveTriggerBindingRecordSource,
    check_exclusive_trigger_binding_record_source,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExclusiveTriggerBindingRecord")


@_attrs_define
class ExclusiveTriggerBindingRecord:
    """Persisted binding between an account-owned trigger or recurring Job schedule and its exclusive-operation policy.
    Exactly one of app_id or job_id is present.

    """

    source: ExclusiveTriggerBindingRecordSource
    trigger_id: UUID
    policy: str
    key: bool | float | str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    app_id: UUID | Unset = UNSET
    """Present for app-owned triggers."""
    job_id: UUID | Unset = UNSET
    """Present for a recurring Job schedule."""
    platform_tenant_id: UUID | Unset = UNSET
    equivalence_key: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        trigger_id = str(self.trigger_id)

        policy = self.policy

        key: bool | float | str
        key = self.key

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        equivalence_key = self.equivalence_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "trigger_id": trigger_id,
                "policy": policy,
                "key": key,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if equivalence_key is not UNSET:
            field_dict["equivalence_key"] = equivalence_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_exclusive_trigger_binding_record_source(d.pop("source"))

        trigger_id = UUID(d.pop("trigger_id"))

        policy = d.pop("policy")

        def _parse_key(data: object) -> bool | float | str:
            return cast(bool | float | str, data)

        key = _parse_key(d.pop("key"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        equivalence_key = d.pop("equivalence_key", UNSET)

        exclusive_trigger_binding_record = cls(
            source=source,
            trigger_id=trigger_id,
            policy=policy,
            key=key,
            created_at=created_at,
            updated_at=updated_at,
            app_id=app_id,
            job_id=job_id,
            platform_tenant_id=platform_tenant_id,
            equivalence_key=equivalence_key,
        )

        exclusive_trigger_binding_record.additional_properties = d
        return exclusive_trigger_binding_record

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
