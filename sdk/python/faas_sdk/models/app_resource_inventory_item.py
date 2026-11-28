from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_resource_inventory_binding import AppResourceInventoryBinding


T = TypeVar("T", bound="AppResourceInventoryItem")


@_attrs_define
class AppResourceInventoryItem:
    """Allowlisted resource metadata; optional fields depend on the page kind."""

    id: str
    app_id: str
    scope: str
    name: str | Unset = UNSET
    domain: str | Unset = UNSET
    verified: bool | Unset = UNSET
    cert_status: str | Unset = UNSET
    schedule: str | Unset = UNSET
    path: str | Unset = UNSET
    timezone: str | Unset = UNSET
    enabled: bool | Unset = UNSET
    region: str | Unset = UNSET
    state: str | Unset = UNSET
    postgres_major: int | Unset = UNSET
    bucket_id: str | Unset = UNSET
    bucket: str | Unset = UNSET
    prefix: str | Unset = UNSET
    rotation_pending: bool | Unset = UNSET
    binding: AppResourceInventoryBinding | Unset = UNSET
    """Selected-app database linkage without provider identities or credential references."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        scope = self.scope

        name = self.name

        domain = self.domain

        verified = self.verified

        cert_status = self.cert_status

        schedule = self.schedule

        path = self.path

        timezone = self.timezone

        enabled = self.enabled

        region = self.region

        state = self.state

        postgres_major = self.postgres_major

        bucket_id = self.bucket_id

        bucket = self.bucket

        prefix = self.prefix

        rotation_pending = self.rotation_pending

        binding: dict[str, Any] | Unset = UNSET
        if not isinstance(self.binding, Unset):
            binding = self.binding.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "scope": scope,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name
        if domain is not UNSET:
            field_dict["domain"] = domain
        if verified is not UNSET:
            field_dict["verified"] = verified
        if cert_status is not UNSET:
            field_dict["cert_status"] = cert_status
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if path is not UNSET:
            field_dict["path"] = path
        if timezone is not UNSET:
            field_dict["timezone"] = timezone
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if region is not UNSET:
            field_dict["region"] = region
        if state is not UNSET:
            field_dict["state"] = state
        if postgres_major is not UNSET:
            field_dict["postgres_major"] = postgres_major
        if bucket_id is not UNSET:
            field_dict["bucket_id"] = bucket_id
        if bucket is not UNSET:
            field_dict["bucket"] = bucket
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if rotation_pending is not UNSET:
            field_dict["rotation_pending"] = rotation_pending
        if binding is not UNSET:
            field_dict["binding"] = binding

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_resource_inventory_binding import AppResourceInventoryBinding

        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        scope = d.pop("scope")

        name = d.pop("name", UNSET)

        domain = d.pop("domain", UNSET)

        verified = d.pop("verified", UNSET)

        cert_status = d.pop("cert_status", UNSET)

        schedule = d.pop("schedule", UNSET)

        path = d.pop("path", UNSET)

        timezone = d.pop("timezone", UNSET)

        enabled = d.pop("enabled", UNSET)

        region = d.pop("region", UNSET)

        state = d.pop("state", UNSET)

        postgres_major = d.pop("postgres_major", UNSET)

        bucket_id = d.pop("bucket_id", UNSET)

        bucket = d.pop("bucket", UNSET)

        prefix = d.pop("prefix", UNSET)

        rotation_pending = d.pop("rotation_pending", UNSET)

        _binding = d.pop("binding", UNSET)
        binding: AppResourceInventoryBinding | Unset
        if isinstance(_binding, Unset):
            binding = UNSET
        else:
            binding = AppResourceInventoryBinding.from_dict(_binding)

        app_resource_inventory_item = cls(
            id=id,
            app_id=app_id,
            scope=scope,
            name=name,
            domain=domain,
            verified=verified,
            cert_status=cert_status,
            schedule=schedule,
            path=path,
            timezone=timezone,
            enabled=enabled,
            region=region,
            state=state,
            postgres_major=postgres_major,
            bucket_id=bucket_id,
            bucket=bucket,
            prefix=prefix,
            rotation_pending=rotation_pending,
            binding=binding,
        )

        app_resource_inventory_item.additional_properties = d
        return app_resource_inventory_item

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
