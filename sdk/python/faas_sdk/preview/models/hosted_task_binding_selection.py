from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="HostedTaskBindingSelection")


@_attrs_define
class HostedTaskBindingSelection:
    """Personal-owner selection of native Task binding references for one approved retained source intent, never plaintext
    credentials.

    """

    intent_sha256: str
    review_sha256: str
    namespace: str
    web_database_ref: str
    """Existing read_write native managed PostgreSQL secret reference on the reviewed web app; never a credential
    value."""
    worker_database_ref: str
    """Existing read_write native managed PostgreSQL secret reference on the reviewed worker app sharing the
    selected queue."""
    migration_database_ref: str
    """Worker app reference for its separate migration-only PostgreSQL role; qualification never runs a migration."""
    observer_database_ref: str
    """Native read_only PostgreSQL secret reference on the reviewed observer app, isolated from runtime and
    migration roles."""

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
