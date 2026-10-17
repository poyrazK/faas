from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.project_binding_verification_run_diagnostics import ProjectBindingVerificationRunDiagnostics
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    run: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/{run}/diagnostics".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            run=quote(str(run), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ProjectBindingVerificationRunDiagnostics | None:
    if response.status_code == 200:
        response_200 = ProjectBindingVerificationRunDiagnostics.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ProjectBindingVerificationRunDiagnostics]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ProjectBindingVerificationRunDiagnostics]:
    """Explain verification blockers and safe public changes without admitting work

    Args:
        slug (str):
        environment (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationRunDiagnostics]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        run=run,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ProjectBindingVerificationRunDiagnostics | None:
    """Explain verification blockers and safe public changes without admitting work

    Args:
        slug (str):
        environment (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationRunDiagnostics
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        run=run,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ProjectBindingVerificationRunDiagnostics]:
    """Explain verification blockers and safe public changes without admitting work

    Args:
        slug (str):
        environment (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ProjectBindingVerificationRunDiagnostics]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        run=run,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    run: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ProjectBindingVerificationRunDiagnostics | None:
    """Explain verification blockers and safe public changes without admitting work

    Args:
        slug (str):
        environment (str):
        run (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ProjectBindingVerificationRunDiagnostics
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            run=run,
            client=client,
        )
    ).parsed
