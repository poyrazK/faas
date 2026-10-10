from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_plan_response import APIConsumerPlanResponse
from ...models.problem import Problem
from ...models.update_api_consumer_plan_limits_request import UpdateAPIConsumerPlanLimitsRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    plan_id: UUID,
    *,
    body: UpdateAPIConsumerPlanLimitsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/consumer-plans/{plan_id}".format(
            slug=quote(str(slug), safe=""),
            plan_id=quote(str(plan_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerPlanResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerPlanResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerPlanResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    plan_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAPIConsumerPlanLimitsRequest,
) -> Response[APIConsumerPlanResponse | Problem]:
    """Replace a consumer plan's limits.

     Limits are enforcement, not prices, so they change in place; the gateway applies them within 15
    seconds. Prices change through new rate-card versions.

    Args:
        slug (str):
        plan_id (UUID):
        body (UpdateAPIConsumerPlanLimitsRequest): Replacement limits for a consumer plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerPlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        plan_id=plan_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    plan_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAPIConsumerPlanLimitsRequest,
) -> APIConsumerPlanResponse | Problem | None:
    """Replace a consumer plan's limits.

     Limits are enforcement, not prices, so they change in place; the gateway applies them within 15
    seconds. Prices change through new rate-card versions.

    Args:
        slug (str):
        plan_id (UUID):
        body (UpdateAPIConsumerPlanLimitsRequest): Replacement limits for a consumer plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerPlanResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        plan_id=plan_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    plan_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAPIConsumerPlanLimitsRequest,
) -> Response[APIConsumerPlanResponse | Problem]:
    """Replace a consumer plan's limits.

     Limits are enforcement, not prices, so they change in place; the gateway applies them within 15
    seconds. Prices change through new rate-card versions.

    Args:
        slug (str):
        plan_id (UUID):
        body (UpdateAPIConsumerPlanLimitsRequest): Replacement limits for a consumer plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerPlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        plan_id=plan_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    plan_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateAPIConsumerPlanLimitsRequest,
) -> APIConsumerPlanResponse | Problem | None:
    """Replace a consumer plan's limits.

     Limits are enforcement, not prices, so they change in place; the gateway applies them within 15
    seconds. Prices change through new rate-card versions.

    Args:
        slug (str):
        plan_id (UUID):
        body (UpdateAPIConsumerPlanLimitsRequest): Replacement limits for a consumer plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerPlanResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            plan_id=plan_id,
            client=client,
            body=body,
        )
    ).parsed
