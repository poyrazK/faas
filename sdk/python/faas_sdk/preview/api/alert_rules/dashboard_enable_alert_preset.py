from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.enable_alert_preset_request import EnableAlertPresetRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: EnableAlertPresetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/dashboard/apps/{slug}/alert-presets/{name}/enable".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["data"] = body.to_dict()
    headers["Content-Type"] = "application/x-www-form-urlencoded"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 302:
        response_302 = cast(Any, None)
        return response_302

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
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
    body: EnableAlertPresetRequest,
) -> Response[Any | Problem]:
    """Form-POST sibling of enableAlertPreset for the dashboard.

     Receives application/x-www-form-urlencoded payload from the
    preset-grid form. Coerces the (webhook_url, webhook_secret)
    pair into the same EnableAlertPresetRequest body the JSON
    sibling expects, runs the same plan-tier gate, then 302-redirects
    to /apps/{slug}?just_enabled={rule_id}. The web-cookie auth
    path is sufficient — no MFA challenge (the JSON sibling
    requires MFA via the public-auth middleware).

    Args:
        slug (str):
        name (str):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
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
    body: EnableAlertPresetRequest,
) -> Any | Problem | None:
    """Form-POST sibling of enableAlertPreset for the dashboard.

     Receives application/x-www-form-urlencoded payload from the
    preset-grid form. Coerces the (webhook_url, webhook_secret)
    pair into the same EnableAlertPresetRequest body the JSON
    sibling expects, runs the same plan-tier gate, then 302-redirects
    to /apps/{slug}?just_enabled={rule_id}. The web-cookie auth
    path is sufficient — no MFA challenge (the JSON sibling
    requires MFA via the public-auth middleware).

    Args:
        slug (str):
        name (str):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
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
    body: EnableAlertPresetRequest,
) -> Response[Any | Problem]:
    """Form-POST sibling of enableAlertPreset for the dashboard.

     Receives application/x-www-form-urlencoded payload from the
    preset-grid form. Coerces the (webhook_url, webhook_secret)
    pair into the same EnableAlertPresetRequest body the JSON
    sibling expects, runs the same plan-tier gate, then 302-redirects
    to /apps/{slug}?just_enabled={rule_id}. The web-cookie auth
    path is sufficient — no MFA challenge (the JSON sibling
    requires MFA via the public-auth middleware).

    Args:
        slug (str):
        name (str):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
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
    body: EnableAlertPresetRequest,
) -> Any | Problem | None:
    """Form-POST sibling of enableAlertPreset for the dashboard.

     Receives application/x-www-form-urlencoded payload from the
    preset-grid form. Coerces the (webhook_url, webhook_secret)
    pair into the same EnableAlertPresetRequest body the JSON
    sibling expects, runs the same plan-tier gate, then 302-redirects
    to /apps/{slug}?just_enabled={rule_id}. The web-cookie auth
    path is sufficient — no MFA challenge (the JSON sibling
    requires MFA via the public-auth middleware).

    Args:
        slug (str):
        name (str):
        body (EnableAlertPresetRequest): Body for POST /v1/apps/{slug}/alert-
            presets/{name}/enable.
            The (name, metric, comparison, threshold, window_spec,
            default_cooldown_minutes) sextuple is pre-filled from the
            catalog; the caller supplies the delivery-side fields and an
            optional safe-release action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
