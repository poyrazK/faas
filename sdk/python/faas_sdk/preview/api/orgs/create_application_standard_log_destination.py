from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_log_destination import ApplicationStandardLogDestination
from ...models.create_application_standard_log_destination_request import CreateApplicationStandardLogDestinationRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateApplicationStandardLogDestinationRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/application-standard-log-destinations".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardLogDestination | Problem | None:
    if response.status_code == 201:
        response_201 = ApplicationStandardLogDestination.from_dict(response.json())

        return response_201

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplicationStandardLogDestination | Problem]:
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
    body: CreateApplicationStandardLogDestinationRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardLogDestination | Problem]:
    """Create an immutable organization-owned logging destination.

     Requires org.manage_application_standards to create logging destination references (owner or admin).
    Existing resources cannot be edited. Rotation creates a new reference and
    requires a new standard version to adopt it. Resource creation does not
    enroll or change any service. Customer responses and audit events omit sealed credentials.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardLogDestinationRequest): A logging endpoint and optional
            write-only credential to seal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardLogDestination | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardLogDestinationRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardLogDestination | Problem | None:
    """Create an immutable organization-owned logging destination.

     Requires org.manage_application_standards to create logging destination references (owner or admin).
    Existing resources cannot be edited. Rotation creates a new reference and
    requires a new standard version to adopt it. Resource creation does not
    enroll or change any service. Customer responses and audit events omit sealed credentials.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardLogDestinationRequest): A logging endpoint and optional
            write-only credential to seal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardLogDestination | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardLogDestinationRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardLogDestination | Problem]:
    """Create an immutable organization-owned logging destination.

     Requires org.manage_application_standards to create logging destination references (owner or admin).
    Existing resources cannot be edited. Rotation creates a new reference and
    requires a new standard version to adopt it. Resource creation does not
    enroll or change any service. Customer responses and audit events omit sealed credentials.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardLogDestinationRequest): A logging endpoint and optional
            write-only credential to seal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardLogDestination | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardLogDestinationRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardLogDestination | Problem | None:
    """Create an immutable organization-owned logging destination.

     Requires org.manage_application_standards to create logging destination references (owner or admin).
    Existing resources cannot be edited. Rotation creates a new reference and
    requires a new standard version to adopt it. Resource creation does not
    enroll or change any service. Customer responses and audit events omit sealed credentials.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardLogDestinationRequest): A logging endpoint and optional
            write-only credential to seal.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardLogDestination | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
