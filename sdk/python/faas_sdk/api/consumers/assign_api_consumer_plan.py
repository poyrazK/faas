from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_plan_assignment_response import APIConsumerPlanAssignmentResponse
from ...models.assign_api_consumer_plan_request import AssignAPIConsumerPlanRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    *,
    body: AssignAPIConsumerPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/plan-assignments".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerPlanAssignmentResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APIConsumerPlanAssignmentResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerPlanAssignmentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AssignAPIConsumerPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerPlanAssignmentResponse | Problem]:
    """Move a consumer onto a plan from a UTC minute.

     The assignment takes effect from effective_from (default the next minute; never in the past) and
    requires the target plan to have a rate card in force then. Statements price each minute with the
    plan in force; monthly allowances and tiers keep counting across the change. An empty plan_id
    returns the consumer to the default plan.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (AssignAPIConsumerPlanRequest): Plan assignment; an empty plan_id returns the
            consumer to the default plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerPlanAssignmentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AssignAPIConsumerPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerPlanAssignmentResponse | Problem | None:
    """Move a consumer onto a plan from a UTC minute.

     The assignment takes effect from effective_from (default the next minute; never in the past) and
    requires the target plan to have a rate card in force then. Statements price each minute with the
    plan in force; monthly allowances and tiers keep counting across the change. An empty plan_id
    returns the consumer to the default plan.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (AssignAPIConsumerPlanRequest): Plan assignment; an empty plan_id returns the
            consumer to the default plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerPlanAssignmentResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AssignAPIConsumerPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[APIConsumerPlanAssignmentResponse | Problem]:
    """Move a consumer onto a plan from a UTC minute.

     The assignment takes effect from effective_from (default the next minute; never in the past) and
    requires the target plan to have a rate card in force then. Statements price each minute with the
    plan in force; monthly allowances and tiers keep counting across the change. An empty plan_id
    returns the consumer to the default plan.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (AssignAPIConsumerPlanRequest): Plan assignment; an empty plan_id returns the
            consumer to the default plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerPlanAssignmentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: AssignAPIConsumerPlanRequest,
    idempotency_key: str | Unset = UNSET,
) -> APIConsumerPlanAssignmentResponse | Problem | None:
    """Move a consumer onto a plan from a UTC minute.

     The assignment takes effect from effective_from (default the next minute; never in the past) and
    requires the target plan to have a rate card in force then. Statements price each minute with the
    plan in force; monthly allowances and tiers keep counting across the change. An empty plan_id
    returns the consumer to the default plan.

    Args:
        slug (str):
        consumer_id (UUID):
        idempotency_key (str | Unset):
        body (AssignAPIConsumerPlanRequest): Plan assignment; an empty plan_id returns the
            consumer to the default plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerPlanAssignmentResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
