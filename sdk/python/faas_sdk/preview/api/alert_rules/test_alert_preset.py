from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.test_alert_preset_response import TestAlertPresetResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: Any | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/alert-presets/{name}/test".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TestAlertPresetResponse | None:
    if response.status_code == 200:
        response_200 = TestAlertPresetResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TestAlertPresetResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Response[Problem | TestAlertPresetResponse]:
    """Send a synthetic test alert to the instantiated rule's webhook.

     Test-fires the customer's instantiated alert_preset rule
    against the webhook URL they configured at enable-time.
    The synthetic Event body carries `payload.test = true`
    so the customer's verifier can branch on the discriminator
    (skip the production alert-write path, log to a quieter
    channel, etc.) and a synthetic `observed` value JUST PAST
    the preset's threshold — `threshold × 1.01` for `gt`
    comparisons, `threshold × 0.99` for `lt` (a naive
    threshold × 1.01 for `lt` would land on the wrong side of
    the threshold; the handler's branch-swap is load-bearing).

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 404 preset_not_enabled when the customer has not yet
    instantiated this preset for this app → 502 webhook
    delivery failed after retry exhaustion.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TestAlertPresetResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Problem | TestAlertPresetResponse | None:
    """Send a synthetic test alert to the instantiated rule's webhook.

     Test-fires the customer's instantiated alert_preset rule
    against the webhook URL they configured at enable-time.
    The synthetic Event body carries `payload.test = true`
    so the customer's verifier can branch on the discriminator
    (skip the production alert-write path, log to a quieter
    channel, etc.) and a synthetic `observed` value JUST PAST
    the preset's threshold — `threshold × 1.01` for `gt`
    comparisons, `threshold × 0.99` for `lt` (a naive
    threshold × 1.01 for `lt` would land on the wrong side of
    the threshold; the handler's branch-swap is load-bearing).

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 404 preset_not_enabled when the customer has not yet
    instantiated this preset for this app → 502 webhook
    delivery failed after retry exhaustion.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TestAlertPresetResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Response[Problem | TestAlertPresetResponse]:
    """Send a synthetic test alert to the instantiated rule's webhook.

     Test-fires the customer's instantiated alert_preset rule
    against the webhook URL they configured at enable-time.
    The synthetic Event body carries `payload.test = true`
    so the customer's verifier can branch on the discriminator
    (skip the production alert-write path, log to a quieter
    channel, etc.) and a synthetic `observed` value JUST PAST
    the preset's threshold — `threshold × 1.01` for `gt`
    comparisons, `threshold × 0.99` for `lt` (a naive
    threshold × 1.01 for `lt` would land on the wrong side of
    the threshold; the handler's branch-swap is load-bearing).

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 404 preset_not_enabled when the customer has not yet
    instantiated this preset for this app → 502 webhook
    delivery failed after retry exhaustion.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TestAlertPresetResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Problem | TestAlertPresetResponse | None:
    """Send a synthetic test alert to the instantiated rule's webhook.

     Test-fires the customer's instantiated alert_preset rule
    against the webhook URL they configured at enable-time.
    The synthetic Event body carries `payload.test = true`
    so the customer's verifier can branch on the discriminator
    (skip the production alert-write path, log to a quieter
    channel, etc.) and a synthetic `observed` value JUST PAST
    the preset's threshold — `threshold × 1.01` for `gt`
    comparisons, `threshold × 0.99` for `lt` (a naive
    threshold × 1.01 for `lt` would land on the wrong side of
    the threshold; the handler's branch-swap is load-bearing).

    Pre-loadApp gates fire in this order: 404 on missing
    preset → 400 alert_preset_disabled on disabled-in-catalog
    → 402 plan_alert_presets_not_allowed on below-minimum-plan
    → 404 preset_not_enabled when the customer has not yet
    instantiated this preset for this app → 502 webhook
    delivery failed after retry exhaustion.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TestAlertPresetResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
