from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_release_webhook_response import AccountReleaseWebhookResponse
from ...models.problem import Problem
from ...models.update_account_release_webhook_request import UpdateAccountReleaseWebhookRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: UpdateAccountReleaseWebhookRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account/release-webhooks/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountReleaseWebhookResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountReleaseWebhookResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountReleaseWebhookResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountReleaseWebhookRequest,
) -> Response[AccountReleaseWebhookResponse | Problem]:
    """Update an account release receiver.

    Args:
        id (str):
        body (UpdateAccountReleaseWebhookRequest): Partial update; omitted fields retain their
            current value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountReleaseWebhookResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountReleaseWebhookRequest,
) -> AccountReleaseWebhookResponse | Problem | None:
    """Update an account release receiver.

    Args:
        id (str):
        body (UpdateAccountReleaseWebhookRequest): Partial update; omitted fields retain their
            current value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountReleaseWebhookResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountReleaseWebhookRequest,
) -> Response[AccountReleaseWebhookResponse | Problem]:
    """Update an account release receiver.

    Args:
        id (str):
        body (UpdateAccountReleaseWebhookRequest): Partial update; omitted fields retain their
            current value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountReleaseWebhookResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAccountReleaseWebhookRequest,
) -> AccountReleaseWebhookResponse | Problem | None:
    """Update an account release receiver.

    Args:
        id (str):
        body (UpdateAccountReleaseWebhookRequest): Partial update; omitted fields retain their
            current value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountReleaseWebhookResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
