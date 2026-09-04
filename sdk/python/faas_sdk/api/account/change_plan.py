from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_response import AccountResponse
from ...models.change_plan_request import ChangePlanRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ChangePlanRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/account/plan",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountResponse.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = AccountResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ChangePlanRequest,
) -> Response[AccountResponse | Problem]:
    """Change billing plan after provider confirmation.

     Switch the account between `free`, `hobby`, `pro`, and `scale`. The
    local account moves to a paid tier only after the configured billing
    provider confirms payment. If a new subscription is required, the
    `payment_required` response includes `checkout_url`; if an existing
    subscription must be changed, it includes `billing_portal_url`.

    Args:
        body (ChangePlanRequest): Target plan for the change.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ChangePlanRequest,
) -> AccountResponse | Problem | None:
    """Change billing plan after provider confirmation.

     Switch the account between `free`, `hobby`, `pro`, and `scale`. The
    local account moves to a paid tier only after the configured billing
    provider confirms payment. If a new subscription is required, the
    `payment_required` response includes `checkout_url`; if an existing
    subscription must be changed, it includes `billing_portal_url`.

    Args:
        body (ChangePlanRequest): Target plan for the change.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ChangePlanRequest,
) -> Response[AccountResponse | Problem]:
    """Change billing plan after provider confirmation.

     Switch the account between `free`, `hobby`, `pro`, and `scale`. The
    local account moves to a paid tier only after the configured billing
    provider confirms payment. If a new subscription is required, the
    `payment_required` response includes `checkout_url`; if an existing
    subscription must be changed, it includes `billing_portal_url`.

    Args:
        body (ChangePlanRequest): Target plan for the change.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ChangePlanRequest,
) -> AccountResponse | Problem | None:
    """Change billing plan after provider confirmation.

     Switch the account between `free`, `hobby`, `pro`, and `scale`. The
    local account moves to a paid tier only after the configured billing
    provider confirms payment. If a new subscription is required, the
    `payment_required` response includes `checkout_url`; if an existing
    subscription must be changed, it includes `billing_portal_url`.

    Args:
        body (ChangePlanRequest): Target plan for the change.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
