from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.send_managed_realtime_principal_body import SendManagedRealtimePrincipalBody
from ...models.send_managed_realtime_principal_response_202 import SendManagedRealtimePrincipalResponse202
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: SendManagedRealtimePrincipalBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/principals:send".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SendManagedRealtimePrincipalResponse202 | None:
    if response.status_code == 202:
        response_202 = SendManagedRealtimePrincipalResponse202.from_dict(response.json())

        return response_202

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[Problem | SendManagedRealtimePrincipalResponse202]:
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
    body: SendManagedRealtimePrincipalBody,
) -> Response[Problem | SendManagedRealtimePrincipalResponse202]:
    """Send to a verified principal, optionally retaining a notification with push fallback.

     Retained sends require the retained-history preview gate and a stable message ID. Notification
    category is part of deduplication and defaults to notifications. Push preferences are evaluated
    before provider delivery; ACKs cancel queued fallback. Live sends can request receipts instead of
    retention.

    Args:
        slug (str):
        id (str):
        body (SendManagedRealtimePrincipalBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SendManagedRealtimePrincipalResponse202]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
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
    body: SendManagedRealtimePrincipalBody,
) -> Problem | SendManagedRealtimePrincipalResponse202 | None:
    """Send to a verified principal, optionally retaining a notification with push fallback.

     Retained sends require the retained-history preview gate and a stable message ID. Notification
    category is part of deduplication and defaults to notifications. Push preferences are evaluated
    before provider delivery; ACKs cancel queued fallback. Live sends can request receipts instead of
    retention.

    Args:
        slug (str):
        id (str):
        body (SendManagedRealtimePrincipalBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SendManagedRealtimePrincipalResponse202
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SendManagedRealtimePrincipalBody,
) -> Response[Problem | SendManagedRealtimePrincipalResponse202]:
    """Send to a verified principal, optionally retaining a notification with push fallback.

     Retained sends require the retained-history preview gate and a stable message ID. Notification
    category is part of deduplication and defaults to notifications. Push preferences are evaluated
    before provider delivery; ACKs cancel queued fallback. Live sends can request receipts instead of
    retention.

    Args:
        slug (str):
        id (str):
        body (SendManagedRealtimePrincipalBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SendManagedRealtimePrincipalResponse202]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SendManagedRealtimePrincipalBody,
) -> Problem | SendManagedRealtimePrincipalResponse202 | None:
    """Send to a verified principal, optionally retaining a notification with push fallback.

     Retained sends require the retained-history preview gate and a stable message ID. Notification
    category is part of deduplication and defaults to notifications. Push preferences are evaluated
    before provider delivery; ACKs cancel queued fallback. Live sends can request receipts instead of
    retention.

    Args:
        slug (str):
        id (str):
        body (SendManagedRealtimePrincipalBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SendManagedRealtimePrincipalResponse202
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
