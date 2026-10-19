from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_trial_response import APIConsumerTrialResponse
from ...models.problem import Problem
from ...models.put_api_consumer_trial_request import PutAPIConsumerTrialRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    *,
    body: PutAPIConsumerTrialRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/trial".format(
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
) -> APIConsumerTrialResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerTrialResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerTrialResponse | Problem]:
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
    body: PutAPIConsumerTrialRequest,
) -> Response[APIConsumerTrialResponse | Problem]:
    """Start a consumer's one free trial, or move the end of a running one.

     Usage in the trial is priced at zero and consumes no included units.
    A new trial starts at the next UTC minute; a running trial's end can
    move, never before the next minute. A consumer whose trial has ended
    cannot get another (409).

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerTrialResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
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
    body: PutAPIConsumerTrialRequest,
) -> APIConsumerTrialResponse | Problem | None:
    """Start a consumer's one free trial, or move the end of a running one.

     Usage in the trial is priced at zero and consumes no included units.
    A new trial starts at the next UTC minute; a running trial's end can
    move, never before the next minute. A consumer whose trial has ended
    cannot get another (409).

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerTrialResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerTrialRequest,
) -> Response[APIConsumerTrialResponse | Problem]:
    """Start a consumer's one free trial, or move the end of a running one.

     Usage in the trial is priced at zero and consumes no included units.
    A new trial starts at the next UTC minute; a running trial's end can
    move, never before the next minute. A consumer whose trial has ended
    cannot get another (409).

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerTrialResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerTrialRequest,
) -> APIConsumerTrialResponse | Problem | None:
    """Start a consumer's one free trial, or move the end of a running one.

     Usage in the trial is priced at zero and consumes no included units.
    A new trial starts at the next UTC minute; a running trial's end can
    move, never before the next minute. A consumer whose trial has ended
    cannot get another (409).

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerTrialRequest): A consumer's trial length in days from its start, or
            its end (ADR-1314). Set exactly one.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerTrialResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            client=client,
            body=body,
        )
    ).parsed
