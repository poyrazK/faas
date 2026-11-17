from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="HostedTaskBindingSelection")


@_attrs_define
class HostedTaskBindingSelection:
    intent_sha256: str
    review_sha256: str
    namespace: str
    web_database_ref: str
    """Owner-only native managed PostgreSQL reference; no credential value."""
    worker_database_ref: str
    """Owner-only native managed PostgreSQL reference; no credential value."""
    migration_database_ref: str
    """Owner-only native managed PostgreSQL reference; no credential value."""
    observer_database_ref: str
    """Owner-only native managed PostgreSQL reference; no credential value."""

    def to_dict(self) -> dict[str, Any]:
        intent_sha256 = self.intent_sha256

        review_sha256 = self.review_sha256

        namespace = self.namespace

        web_database_ref = self.web_database_ref

        worker_database_ref = self.worker_database_ref

        migration_database_ref = self.migration_database_ref

        observer_database_ref = self.observer_database_ref

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_sha256": intent_sha256,
                "review_sha256": review_sha256,
                "namespace": namespace,
                "web_database_ref": web_database_ref,
                "worker_database_ref": worker_database_ref,
                "migration_database_ref": migration_database_ref,
                "observer_database_ref": observer_database_ref,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        intent_sha256 = d.pop("intent_sha256")

        review_sha256 = d.pop("review_sha256")

        namespace = d.pop("namespace")

        web_database_ref = d.pop("web_database_ref")

        worker_database_ref = d.pop("worker_database_ref")

        migration_database_ref = d.pop("migration_database_ref")

        observer_database_ref = d.pop("observer_database_ref")

        hosted_task_binding_selection = cls(
            intent_sha256=intent_sha256,
            review_sha256=review_sha256,
            namespace=namespace,
            web_database_ref=web_database_ref,
            worker_database_ref=worker_database_ref,
            migration_database_ref=migration_database_ref,
            observer_database_ref=observer_database_ref,
        )

        return hosted_task_binding_selection
