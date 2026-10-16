from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_project_binding_verification_run_request import CreateProjectBindingVerificationRunRequest
from ...models.project_binding_verification_run import ProjectBindingVerificationRun
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: CreateProjectBindingVerificationRunRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ProjectBindingVerificationRun | None:
    if response.status_code == 201:
        response_201 = ProjectBindingVerificationRun.from_dict(response.json())

        return response_201

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ProjectBindingVerificationRun]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectBindingVerificationRunRequest,
) -> Response[Any | ProjectBindingVerificationRun]:
    """createProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        body (CreateProjectBindingVerificationRunRequest): Reviewed project selection, consent and
            shared concurrency for creating a durable verification graph.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationRun]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectBindingVerificationRunRequest,
) -> Any | ProjectBindingVerificationRun | None:
    """createProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        body (CreateProjectBindingVerificationRunRequest): Reviewed project selection, consent and
            shared concurrency for creating a durable verification graph.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationRun
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectBindingVerificationRunRequest,
) -> Response[Any | ProjectBindingVerificationRun]:
    """createProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        body (CreateProjectBindingVerificationRunRequest): Reviewed project selection, consent and
            shared concurrency for creating a durable verification graph.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationRun]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateProjectBindingVerificationRunRequest,
) -> Any | ProjectBindingVerificationRun | None:
    """createProjectBindingVerificationRun

    Args:
        slug (str):
        environment (str):
        body (CreateProjectBindingVerificationRunRequest): Reviewed project selection, consent and
            shared concurrency for creating a durable verification graph.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationRun
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
