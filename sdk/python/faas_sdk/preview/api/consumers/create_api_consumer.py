from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_response import APIConsumerResponse
from ...models.create_api_consumer_request import CreateAPIConsumerRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateAPIConsumerRequest,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/consumers".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APIConsumerResponse.from_dict(response.json())

        return response_201

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRequest,
    environment: str | Unset = UNSET,
) -> Response[APIConsumerResponse | Problem]:
    """Register a stable API consumer identity.

     With `environment` set to a stage, the consumer and its keys belong
    to that stage: the stage's hosts accept only its own consumers once it
    has any, and production's until then. Plan assignments follow the
    consumer; trials and billing apply to production consumers only. A
    stage consumer may reuse a production `external_ref` and key names.
    Creating a stage's consumer answers 501 `not_implemented` until the
    platform enables it.

    Args:
        slug (str):
        environment (str | Unset):
        body (CreateAPIConsumerRequest): Stable customer identity to which consumer credentials
            can be attached.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRequest,
    environment: str | Unset = UNSET,
) -> APIConsumerResponse | Problem | None:
    """Register a stable API consumer identity.

     With `environment` set to a stage, the consumer and its keys belong
    to that stage: the stage's hosts accept only its own consumers once it
    has any, and production's until then. Plan assignments follow the
    consumer; trials and billing apply to production consumers only. A
    stage consumer may reuse a production `external_ref` and key names.
    Creating a stage's consumer answers 501 `not_implemented` until the
    platform enables it.

    Args:
        slug (str):
        environment (str | Unset):
        body (CreateAPIConsumerRequest): Stable customer identity to which consumer credentials
            can be attached.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRequest,
    environment: str | Unset = UNSET,
) -> Response[APIConsumerResponse | Problem]:
    """Register a stable API consumer identity.

     With `environment` set to a stage, the consumer and its keys belong
    to that stage: the stage's hosts accept only its own consumers once it
    has any, and production's until then. Plan assignments follow the
    consumer; trials and billing apply to production consumers only. A
    stage consumer may reuse a production `external_ref` and key names.
    Creating a stage's consumer answers 501 `not_implemented` until the
    platform enables it.

    Args:
        slug (str):
        environment (str | Unset):
        body (CreateAPIConsumerRequest): Stable customer identity to which consumer credentials
            can be attached.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateAPIConsumerRequest,
    environment: str | Unset = UNSET,
) -> APIConsumerResponse | Problem | None:
    """Register a stable API consumer identity.

     With `environment` set to a stage, the consumer and its keys belong
    to that stage: the stage's hosts accept only its own consumers once it
    has any, and production's until then. Plan assignments follow the
    consumer; trials and billing apply to production consumers only. A
    stage consumer may reuse a production `external_ref` and key names.
    Creating a stage's consumer answers 501 `not_implemented` until the
    platform enables it.

    Args:
        slug (str):
        environment (str | Unset):
        body (CreateAPIConsumerRequest): Stable customer identity to which consumer credentials
            can be attached.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            environment=environment,
        )
    ).parsed
