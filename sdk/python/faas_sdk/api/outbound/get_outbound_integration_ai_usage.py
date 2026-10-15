import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_usage_response import OutboundAIUsageResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    integration: UUID,
    *,
    date: datetime.date | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_date: str | Unset = UNSET
    if not isinstance(date, Unset):
        json_date = date.isoformat()
    params["date"] = json_date

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-usage".format(
            integration=quote(str(integration), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIUsageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAIUsageResponse.from_dict(response.json())

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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAIUsageResponse | Problem]:
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
    date: datetime.date | Unset = UNSET,
) -> Response[OutboundAIUsageResponse | Problem]:
    """Read one UTC day of AI gateway token usage by caller and model.

     Requires MFA and read scope. Rollups are kept for 90 days.

    Args:
        integration (UUID):
        date (datetime.date | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        date=date,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    date: datetime.date | Unset = UNSET,
) -> OutboundAIUsageResponse | Problem | None:
    """Read one UTC day of AI gateway token usage by caller and model.

     Requires MFA and read scope. Rollups are kept for 90 days.

    Args:
        integration (UUID):
        date (datetime.date | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIUsageResponse | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        date=date,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    date: datetime.date | Unset = UNSET,
) -> Response[OutboundAIUsageResponse | Problem]:
    """Read one UTC day of AI gateway token usage by caller and model.

     Requires MFA and read scope. Rollups are kept for 90 days.

    Args:
        integration (UUID):
        date (datetime.date | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        date=date,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    date: datetime.date | Unset = UNSET,
) -> OutboundAIUsageResponse | Problem | None:
    """Read one UTC day of AI gateway token usage by caller and model.

     Requires MFA and read scope. Rollups are kept for 90 days.

    Args:
        integration (UUID):
        date (datetime.date | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            date=date,
        )
    ).parsed
