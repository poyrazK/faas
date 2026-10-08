from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.realtime_notification_preferences import RealtimeNotificationPreferences
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: RealtimeNotificationPreferences,
    principal: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["principal"] = principal

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/push/preferences".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RealtimeNotificationPreferences | None:
    if response.status_code == 200:
        response_200 = RealtimeNotificationPreferences.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RealtimeNotificationPreferences]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RealtimeNotificationPreferences,
    principal: str,
) -> Response[Problem | RealtimeNotificationPreferences]:
    """Replace notification preferences across user devices.

     Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences
    apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not
    consume attempts. At most 256 preference documents per endpoint.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (RealtimeNotificationPreferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeNotificationPreferences]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        principal=principal,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RealtimeNotificationPreferences,
    principal: str,
) -> Problem | RealtimeNotificationPreferences | None:
    """Replace notification preferences across user devices.

     Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences
    apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not
    consume attempts. At most 256 preference documents per endpoint.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (RealtimeNotificationPreferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeNotificationPreferences
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
        principal=principal,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RealtimeNotificationPreferences,
    principal: str,
) -> Response[Problem | RealtimeNotificationPreferences]:
    """Replace notification preferences across user devices.

     Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences
    apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not
    consume attempts. At most 256 preference documents per endpoint.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (RealtimeNotificationPreferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RealtimeNotificationPreferences]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        principal=principal,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RealtimeNotificationPreferences,
    principal: str,
) -> Problem | RealtimeNotificationPreferences | None:
    """Replace notification preferences across user devices.

     Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences
    apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not
    consume attempts. At most 256 preference documents per endpoint.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (RealtimeNotificationPreferences):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RealtimeNotificationPreferences
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
            principal=principal,
        )
    ).parsed
