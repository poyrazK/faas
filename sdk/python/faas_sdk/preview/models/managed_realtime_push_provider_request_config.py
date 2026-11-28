from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_realtime_push_provider_request_config_provider import (
    ManagedRealtimePushProviderRequestConfigProvider,
    check_managed_realtime_push_provider_request_config_provider,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_push_provider_request_config_service_account_json import (
        ManagedRealtimePushProviderRequestConfigServiceAccountJson,
    )


T = TypeVar("T", bound="ManagedRealtimePushProviderRequestConfig")


@_attrs_define
class ManagedRealtimePushProviderRequestConfig:
    provider: ManagedRealtimePushProviderRequestConfigProvider
    project_id: str | Unset = UNSET
    service_account_json: ManagedRealtimePushProviderRequestConfigServiceAccountJson | Unset = UNSET
    """FCM service-account JSON with Firebase Messaging permission."""
    team_id: str | Unset = UNSET
    key_id: str | Unset = UNSET
    topic: str | Unset = UNSET
    private_key: str | Unset = UNSET
    """APNs PKCS8 PEM key or Web Push unpadded base64url P-256 private scalar."""
    sandbox: bool | Unset = False
    subject: str | Unset = UNSET
    """VAPID mailto or https contact URI."""
    title: str | Unset = UNSET
    body: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        project_id = self.project_id

        service_account_json: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_account_json, Unset):
            service_account_json = self.service_account_json.to_dict()

        team_id = self.team_id

        key_id = self.key_id

        topic = self.topic

        private_key = self.private_key

        sandbox = self.sandbox

        subject = self.subject

        title = self.title

        body = self.body

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "provider": provider,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if service_account_json is not UNSET:
            field_dict["service_account_json"] = service_account_json
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if key_id is not UNSET:
            field_dict["key_id"] = key_id
        if topic is not UNSET:
            field_dict["topic"] = topic
        if private_key is not UNSET:
            field_dict["private_key"] = private_key
        if sandbox is not UNSET:
            field_dict["sandbox"] = sandbox
        if subject is not UNSET:
            field_dict["subject"] = subject
        if title is not UNSET:
            field_dict["title"] = title
        if body is not UNSET:
            field_dict["body"] = body

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_push_provider_request_config_service_account_json import (
            ManagedRealtimePushProviderRequestConfigServiceAccountJson,
        )

        d = dict(src_dict)
        provider = check_managed_realtime_push_provider_request_config_provider(d.pop("provider"))

        project_id = d.pop("project_id", UNSET)

        _service_account_json = d.pop("service_account_json", UNSET)
        service_account_json: ManagedRealtimePushProviderRequestConfigServiceAccountJson | Unset
        if isinstance(_service_account_json, Unset):
            service_account_json = UNSET
        else:
            service_account_json = ManagedRealtimePushProviderRequestConfigServiceAccountJson.from_dict(
                _service_account_json
            )

        team_id = d.pop("team_id", UNSET)

        key_id = d.pop("key_id", UNSET)

        topic = d.pop("topic", UNSET)

        private_key = d.pop("private_key", UNSET)

        sandbox = d.pop("sandbox", UNSET)

        subject = d.pop("subject", UNSET)

        title = d.pop("title", UNSET)

        body = d.pop("body", UNSET)

        managed_realtime_push_provider_request_config = cls(
            provider=provider,
            project_id=project_id,
            service_account_json=service_account_json,
            team_id=team_id,
            key_id=key_id,
            topic=topic,
            private_key=private_key,
            sandbox=sandbox,
            subject=subject,
            title=title,
            body=body,
        )

        return managed_realtime_push_provider_request_config
