from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_error_summary_item_error_class import (
    AppErrorSummaryItemErrorClass,
    check_app_error_summary_item_error_class,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppErrorSummaryItem")


@_attrs_define
class AppErrorSummaryItem:
    """One row of the errors summary (ADR-096 / PR-B)."""

    fingerprint: str
    """64-hex-char SHA-256 fingerprint identifying this error group; stable across requests with the same (route,
    status, error_class)."""
    error_class: AppErrorSummaryItemErrorClass
    """Closed-vocabulary error class. 'unhandled' is collapsed under 'Other' in the UI; the DB stores the precise
    class."""
    route: str
    """Matched route template (e.g. `/users/{id}`), NEVER the expanded URL."""
    http_status: int
    count: int
    """Issue count (deduped within AppErrorsDedupeWindowSeconds=3600)."""
    request_count: int
    """Total distinct request rows for this fingerprint."""
    first_seen_at: datetime.datetime
    last_seen_at: datetime.datetime
    sample_message: str
    """PII-redacted sample message (already-redacted at write time; ≤AppErrorsSampleMessageCapBytes=512 bytes)."""
    last_instance_id: str | Unset = UNSET
    """Most recent platform instance that produced this fingerprint."""
    last_node_id: str | Unset = UNSET
    """Most recent compute node that produced this fingerprint."""
    last_region: str | Unset = UNSET
    last_commit_sha: str | Unset = UNSET
    last_deployment_tag: str | Unset = UNSET
    last_deployment_created_at: str | Unset = UNSET
    last_image_digest: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        fingerprint = self.fingerprint

        error_class: str = self.error_class

        route = self.route

        http_status = self.http_status

        count = self.count

        request_count = self.request_count

        first_seen_at = self.first_seen_at.isoformat()

        last_seen_at = self.last_seen_at.isoformat()

        sample_message = self.sample_message

        last_instance_id = self.last_instance_id

        last_node_id = self.last_node_id

        last_region = self.last_region

        last_commit_sha = self.last_commit_sha

        last_deployment_tag = self.last_deployment_tag

        last_deployment_created_at = self.last_deployment_created_at

        last_image_digest = self.last_image_digest

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "fingerprint": fingerprint,
                "error_class": error_class,
                "route": route,
                "http_status": http_status,
                "count": count,
                "request_count": request_count,
                "first_seen_at": first_seen_at,
                "last_seen_at": last_seen_at,
                "sample_message": sample_message,
            }
        )
        if last_instance_id is not UNSET:
            field_dict["last_instance_id"] = last_instance_id
        if last_node_id is not UNSET:
            field_dict["last_node_id"] = last_node_id
        if last_region is not UNSET:
            field_dict["last_region"] = last_region
        if last_commit_sha is not UNSET:
            field_dict["last_commit_sha"] = last_commit_sha
        if last_deployment_tag is not UNSET:
            field_dict["last_deployment_tag"] = last_deployment_tag
        if last_deployment_created_at is not UNSET:
            field_dict["last_deployment_created_at"] = last_deployment_created_at
        if last_image_digest is not UNSET:
            field_dict["last_image_digest"] = last_image_digest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        fingerprint = d.pop("fingerprint")

        error_class = check_app_error_summary_item_error_class(d.pop("error_class"))

        route = d.pop("route")

        http_status = d.pop("http_status")

        count = d.pop("count")

        request_count = d.pop("request_count")

        first_seen_at = datetime.datetime.fromisoformat(d.pop("first_seen_at"))

        last_seen_at = datetime.datetime.fromisoformat(d.pop("last_seen_at"))

        sample_message = d.pop("sample_message")

        last_instance_id = d.pop("last_instance_id", UNSET)

        last_node_id = d.pop("last_node_id", UNSET)

        last_region = d.pop("last_region", UNSET)

        last_commit_sha = d.pop("last_commit_sha", UNSET)

        last_deployment_tag = d.pop("last_deployment_tag", UNSET)

        last_deployment_created_at = d.pop("last_deployment_created_at", UNSET)

        last_image_digest = d.pop("last_image_digest", UNSET)

        app_error_summary_item = cls(
            fingerprint=fingerprint,
            error_class=error_class,
            route=route,
            http_status=http_status,
            count=count,
            request_count=request_count,
            first_seen_at=first_seen_at,
            last_seen_at=last_seen_at,
            sample_message=sample_message,
            last_instance_id=last_instance_id,
            last_node_id=last_node_id,
            last_region=last_region,
            last_commit_sha=last_commit_sha,
            last_deployment_tag=last_deployment_tag,
            last_deployment_created_at=last_deployment_created_at,
            last_image_digest=last_image_digest,
        )

        app_error_summary_item.additional_properties = d
        return app_error_summary_item

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
