from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.put_outbound_daily_request_budget_request import PutOutboundDailyRequestBudgetRequest
from ...types import Response


def _get_kwargs(
    integration: UUID,
    *,
    body: PutOutboundDailyRequestBudgetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/integrations/{integration}/budget".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

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
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundDailyRequestBudgetRequest,
) -> Response[Any | Problem]:
    """Set or clear a customer-owned integration's daily request limit.

     Requires MFA and deploy-write scope. The configured value cannot exceed the account plan's per-
    integration ceiling. Set daily_request_limit to null to clear the customer-selected limit.

    Args:
        integration (UUID):
        body (PutOutboundDailyRequestBudgetRequest): Set a lower per-integration daily request
            limit or clear it with null.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundDailyRequestBudgetRequest,
) -> Any | Problem | None:
    """Set or clear a customer-owned integration's daily request limit.

     Requires MFA and deploy-write scope. The configured value cannot exceed the account plan's per-
    integration ceiling. Set daily_request_limit to null to clear the customer-selected limit.

    Args:
        integration (UUID):
        body (PutOutboundDailyRequestBudgetRequest): Set a lower per-integration daily request
            limit or clear it with null.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundDailyRequestBudgetRequest,
) -> Response[Any | Problem]:
    """Set or clear a customer-owned integration's daily request limit.

     Requires MFA and deploy-write scope. The configured value cannot exceed the account plan's per-
    integration ceiling. Set daily_request_limit to null to clear the customer-selected limit.

    Args:
        integration (UUID):
        body (PutOutboundDailyRequestBudgetRequest): Set a lower per-integration daily request
            limit or clear it with null.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundDailyRequestBudgetRequest,
) -> Any | Problem | None:
    """Set or clear a customer-owned integration's daily request limit.

     Requires MFA and deploy-write scope. The configured value cannot exceed the account plan's per-
    integration ceiling. Set daily_request_limit to null to clear the customer-selected limit.

    Args:
        integration (UUID):
        body (PutOutboundDailyRequestBudgetRequest): Set a lower per-integration daily request
            limit or clear it with null.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            body=body,
        )
    ).parsed
