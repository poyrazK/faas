from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentPreviewURL")


@_attrs_define
class DeploymentPreviewURL:
    """Per-deployment preview URL read seam response.
    Issue #976 / ADR-122 / SAFE-RELEASES-C.2.
    Mirrors the api.DeploymentPreviewURL struct in pkg/api/dto.go.

    """

    deployment_id: UUID
    """Echoed from the path so a batch caller can correlate."""
    app_id: UUID
    """Resolved parent app id."""
    alive: bool
    """True iff the deployment exists, belongs to the caller, and has a status in {pending, building, imaging,
    snapshotting, live} (the same predicate as state.Deployment.DeploymentPreviewActive the cert allowlist
    consults)."""
    host: str | Unset = UNSET
    """Per-deployment preview hostname (`deploy-{N}.{slug}.gregale.dev`). Empty when alive=false OR when
    wire.DeployWildcardSuffix is "" (zone disabled)."""
    url: str | Unset = UNSET
    """Full request URL (`https://<host>`). Empty when host is empty."""
    last_checked_at: datetime.datetime | None | Unset = UNSET
    """When certmagic last validated the cert under host. Null for never-touched hostnames. NOT a latency probe —
    the cert NotAfter is the load-bearing expiry."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        app_id = str(self.app_id)

        alive = self.alive

        host = self.host

        url = self.url

        last_checked_at: None | str | Unset
        if isinstance(self.last_checked_at, Unset):
            last_checked_at = UNSET
        elif isinstance(self.last_checked_at, datetime.datetime):
            last_checked_at = self.last_checked_at.isoformat()
        else:
            last_checked_at = self.last_checked_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "app_id": app_id,
                "alive": alive,
            }
        )
        if host is not UNSET:
            field_dict["host"] = host
        if url is not UNSET:
            field_dict["url"] = url
        if last_checked_at is not UNSET:
            field_dict["last_checked_at"] = last_checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        app_id = UUID(d.pop("app_id"))

        alive = d.pop("alive")

        host = d.pop("host", UNSET)

        url = d.pop("url", UNSET)

        def _parse_last_checked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_checked_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_checked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_checked_at = _parse_last_checked_at(d.pop("last_checked_at", UNSET))

        deployment_preview_url = cls(
            deployment_id=deployment_id,
            app_id=app_id,
            alive=alive,
            host=host,
            url=url,
            last_checked_at=last_checked_at,
        )

        deployment_preview_url.additional_properties = d
        return deployment_preview_url

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
