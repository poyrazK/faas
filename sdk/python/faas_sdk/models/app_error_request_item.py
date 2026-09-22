from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppErrorRequestItem")


@_attrs_define
class AppErrorRequestItem:
    """One row of the drill-down page (ADR-096 / PR-B)."""

    request_id: str
    received_at: datetime.datetime
    route: str
    http_status: int
    error_class: str
    sample_message: str
    deployment_id: None | str | Unset = UNSET
    """Nullable — the FK is ON DELETE SET NULL so an evicted deployment leaves the drill-down row intact."""
    instance_id: str | Unset = UNSET
    node_id: str | Unset = UNSET
    region: str | Unset = UNSET
    commit_sha: str | Unset = UNSET
    deployment_tag: str | Unset = UNSET
    deployment_created_at: str | Unset = UNSET
    image_digest: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        received_at = self.received_at.isoformat()

        route = self.route

        http_status = self.http_status

        error_class = self.error_class

        sample_message = self.sample_message

        deployment_id: None | str | Unset
        if isinstance(self.deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = self.deployment_id

        instance_id = self.instance_id

        node_id = self.node_id

        region = self.region

        commit_sha = self.commit_sha

        deployment_tag = self.deployment_tag

        deployment_created_at = self.deployment_created_at

        image_digest = self.image_digest

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "received_at": received_at,
                "route": route,
                "http_status": http_status,
                "error_class": error_class,
                "sample_message": sample_message,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if node_id is not UNSET:
            field_dict["node_id"] = node_id
        if region is not UNSET:
            field_dict["region"] = region
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if deployment_tag is not UNSET:
            field_dict["deployment_tag"] = deployment_tag
        if deployment_created_at is not UNSET:
            field_dict["deployment_created_at"] = deployment_created_at
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        route = d.pop("route")

        http_status = d.pop("http_status")

        error_class = d.pop("error_class")

        sample_message = d.pop("sample_message")

        def _parse_deployment_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        deployment_id = _parse_deployment_id(d.pop("deployment_id", UNSET))

        instance_id = d.pop("instance_id", UNSET)

        node_id = d.pop("node_id", UNSET)

        region = d.pop("region", UNSET)

        commit_sha = d.pop("commit_sha", UNSET)

        deployment_tag = d.pop("deployment_tag", UNSET)

        deployment_created_at = d.pop("deployment_created_at", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        app_error_request_item = cls(
            request_id=request_id,
            received_at=received_at,
            route=route,
            http_status=http_status,
            error_class=error_class,
            sample_message=sample_message,
            deployment_id=deployment_id,
            instance_id=instance_id,
            node_id=node_id,
            region=region,
            commit_sha=commit_sha,
            deployment_tag=deployment_tag,
            deployment_created_at=deployment_created_at,
            image_digest=image_digest,
        )

        app_error_request_item.additional_properties = d
        return app_error_request_item

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
