from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_example_request import APISandboxExampleRequest
    from ..models.api_sandbox_outbound_webhook import APISandboxOutboundWebhook
    from ..models.api_sandbox_scenario_guide_seeded_ids import APISandboxScenarioGuideSeededIds
    from ..models.api_sandbox_scenario_input_seed import APISandboxScenarioInputSeed
    from ..models.api_sandbox_stripe_webhook import APISandboxStripeWebhook


T = TypeVar("T", bound="APISandboxScenarioInput")


@_attrs_define
class APISandboxScenarioInput:
    """Editable scenario content, including fixtures and request expectations."""

    name: str
    requests: list[APISandboxExampleRequest]
    seed: APISandboxScenarioInputSeed
    """ Synthetic fixture data of up to 8 KiB. Passed to the sandbox release as GREGALE_API_SANDBOX_SEED. """
    seeded_ids: APISandboxScenarioGuideSeededIds | Unset = UNSET
    """ Synthetic IDs shown to invite recipients. """
    stripe_webhook: APISandboxStripeWebhook | Unset = UNSET
    """ Owner-only synthetic Stripe event fixture for an automated run. """
    outbound_webhook: APISandboxOutboundWebhook | Unset = UNSET
    """ Check a managed async job completion callback to a sandbox-owned receiver. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        seed = self.seed.to_dict()

        seeded_ids: dict[str, Any] | Unset = UNSET
        if not isinstance(self.seeded_ids, Unset):
            seeded_ids = self.seeded_ids.to_dict()

        stripe_webhook: dict[str, Any] | Unset = UNSET
        if not isinstance(self.stripe_webhook, Unset):
            stripe_webhook = self.stripe_webhook.to_dict()

        outbound_webhook: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outbound_webhook, Unset):
            outbound_webhook = self.outbound_webhook.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "requests": requests,
                "seed": seed,
            }
        )
        if seeded_ids is not UNSET:
            field_dict["seeded_ids"] = seeded_ids
        if stripe_webhook is not UNSET:
            field_dict["stripe_webhook"] = stripe_webhook
        if outbound_webhook is not UNSET:
            field_dict["outbound_webhook"] = outbound_webhook

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_example_request import APISandboxExampleRequest
        from ..models.api_sandbox_outbound_webhook import APISandboxOutboundWebhook
        from ..models.api_sandbox_scenario_guide_seeded_ids import APISandboxScenarioGuideSeededIds
        from ..models.api_sandbox_scenario_input_seed import APISandboxScenarioInputSeed
        from ..models.api_sandbox_stripe_webhook import APISandboxStripeWebhook

        d = dict(src_dict)
        name = d.pop("name")

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = APISandboxExampleRequest.from_dict(requests_item_data)

            requests.append(requests_item)

        seed = APISandboxScenarioInputSeed.from_dict(d.pop("seed"))

        _seeded_ids = d.pop("seeded_ids", UNSET)
        seeded_ids: APISandboxScenarioGuideSeededIds | Unset
        if isinstance(_seeded_ids, Unset):
            seeded_ids = UNSET
        else:
            seeded_ids = APISandboxScenarioGuideSeededIds.from_dict(_seeded_ids)

        _stripe_webhook = d.pop("stripe_webhook", UNSET)
        stripe_webhook: APISandboxStripeWebhook | Unset
        if isinstance(_stripe_webhook, Unset):
            stripe_webhook = UNSET
        else:
            stripe_webhook = APISandboxStripeWebhook.from_dict(_stripe_webhook)

        _outbound_webhook = d.pop("outbound_webhook", UNSET)
        outbound_webhook: APISandboxOutboundWebhook | Unset
        if isinstance(_outbound_webhook, Unset):
            outbound_webhook = UNSET
        else:
            outbound_webhook = APISandboxOutboundWebhook.from_dict(_outbound_webhook)

        api_sandbox_scenario_input = cls(
            name=name,
            requests=requests,
            seed=seed,
            seeded_ids=seeded_ids,
            stripe_webhook=stripe_webhook,
            outbound_webhook=outbound_webhook,
        )

        api_sandbox_scenario_input.additional_properties = d
        return api_sandbox_scenario_input

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
