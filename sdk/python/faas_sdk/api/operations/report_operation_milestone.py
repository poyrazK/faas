from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_milestone import OperationMilestone
from ...models.operation_milestone_request import OperationMilestoneRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: OperationMilestoneRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["X-Faas-Invocation-Id"] = x_faas_invocation_id

    headers["X-Gregale-Operation-Attempt"] = str(x_gregale_operation_attempt)

    headers["X-Gregale-Operation-Capability"] = x_gregale_operation_capability

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/operations/{id}/milestones".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationMilestone | Problem | None:
    if response.status_code == 200:
        response_200 = OperationMilestone.from_dict(response.json())

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
) -> Response[OperationMilestone | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationMilestoneRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> Response[OperationMilestone | Problem]:
    """Publish a committed public business milestone.

     Requires a current workload assertion and invocation attempt/capability. Validates the pinned
    schema, then deduplicates by logical Operation and milestone ID across recovery. Identical retries
    return the original fact; changed identity content conflicts. Publication never changes business
    completion or delivery state.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationMilestoneRequest): Public application fact with a stable UUID reused across
            retries and recovery. Payloads are validated against the immutable definition; publication
            does not change business outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationMilestone | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationMilestoneRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> OperationMilestone | Problem | None:
    """Publish a committed public business milestone.

     Requires a current workload assertion and invocation attempt/capability. Validates the pinned
    schema, then deduplicates by logical Operation and milestone ID across recovery. Identical retries
    return the original fact; changed identity content conflicts. Publication never changes business
    completion or delivery state.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationMilestoneRequest): Public application fact with a stable UUID reused across
            retries and recovery. Payloads are validated against the immutable definition; publication
            does not change business outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationMilestone | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationMilestoneRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> Response[OperationMilestone | Problem]:
    """Publish a committed public business milestone.

     Requires a current workload assertion and invocation attempt/capability. Validates the pinned
    schema, then deduplicates by logical Operation and milestone ID across recovery. Identical retries
    return the original fact; changed identity content conflicts. Publication never changes business
    completion or delivery state.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationMilestoneRequest): Public application fact with a stable UUID reused across
            retries and recovery. Payloads are validated against the immutable definition; publication
            does not change business outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationMilestone | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        x_faas_invocation_id=x_faas_invocation_id,
        x_gregale_operation_attempt=x_gregale_operation_attempt,
        x_gregale_operation_capability=x_gregale_operation_capability,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: OperationMilestoneRequest,
    x_faas_invocation_id: UUID,
    x_gregale_operation_attempt: int,
    x_gregale_operation_capability: str,
) -> OperationMilestone | Problem | None:
    """Publish a committed public business milestone.

     Requires a current workload assertion and invocation attempt/capability. Validates the pinned
    schema, then deduplicates by logical Operation and milestone ID across recovery. Identical retries
    return the original fact; changed identity content conflicts. Publication never changes business
    completion or delivery state.

    Args:
        id (UUID):
        x_faas_invocation_id (UUID):
        x_gregale_operation_attempt (int):
        x_gregale_operation_capability (str):
        body (OperationMilestoneRequest): Public application fact with a stable UUID reused across
            retries and recovery. Payloads are validated against the immutable definition; publication
            does not change business outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationMilestone | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            x_faas_invocation_id=x_faas_invocation_id,
            x_gregale_operation_attempt=x_gregale_operation_attempt,
            x_gregale_operation_capability=x_gregale_operation_capability,
        )
    ).parsed
