from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.alert_preset_response import AlertPresetResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/alert-presets",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[AlertPresetResponse] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = AlertPresetResponse.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[AlertPresetResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | list[AlertPresetResponse]]:
    r"""List the alert-preset catalog.

     The catalog is intentionally small and bounded, so no pagination.
    Rows whose enabled_in_catalog=false are returned with the
    flag set so the dashboard can render \"coming soon\" — the
    enable endpoint rejects them with 400 alert_preset_disabled.
    Rows whose minimum_plan is above the caller's plan are
    returned with enabled_in_catalog unchanged so the dashboard
    can render an \"upgrade to <plan>\" hint per row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[AlertPresetResponse]]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | list[AlertPresetResponse] | None:
    r"""List the alert-preset catalog.

     The catalog is intentionally small and bounded, so no pagination.
    Rows whose enabled_in_catalog=false are returned with the
    flag set so the dashboard can render \"coming soon\" — the
    enable endpoint rejects them with 400 alert_preset_disabled.
    Rows whose minimum_plan is above the caller's plan are
    returned with enabled_in_catalog unchanged so the dashboard
    can render an \"upgrade to <plan>\" hint per row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[AlertPresetResponse]
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | list[AlertPresetResponse]]:
    r"""List the alert-preset catalog.

     The catalog is intentionally small and bounded, so no pagination.
    Rows whose enabled_in_catalog=false are returned with the
    flag set so the dashboard can render \"coming soon\" — the
    enable endpoint rejects them with 400 alert_preset_disabled.
    Rows whose minimum_plan is above the caller's plan are
    returned with enabled_in_catalog unchanged so the dashboard
    can render an \"upgrade to <plan>\" hint per row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[AlertPresetResponse]]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | list[AlertPresetResponse] | None:
    r"""List the alert-preset catalog.

     The catalog is intentionally small and bounded, so no pagination.
    Rows whose enabled_in_catalog=false are returned with the
    flag set so the dashboard can render \"coming soon\" — the
    enable endpoint rejects them with 400 alert_preset_disabled.
    Rows whose minimum_plan is above the caller's plan are
    returned with enabled_in_catalog unchanged so the dashboard
    can render an \"upgrade to <plan>\" hint per row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[AlertPresetResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
