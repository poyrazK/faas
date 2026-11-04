from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.migration_cutover import MigrationCutover
from ...models.migration_cutover_action_request import MigrationCutoverActionRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    plan: UUID,
    *,
    body: MigrationCutoverActionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/migration-cutovers/{plan}/actions".format(
            slug=quote(str(slug), safe=""),
            plan=quote(str(plan), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> MigrationCutover | Problem | None:
    if response.status_code == 200:
        response_200 = MigrationCutover.from_dict(response.json())

        return response_200

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MigrationCutover | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
    body: MigrationCutoverActionRequest,
) -> Response[MigrationCutover | Problem]:
    """Start checks, complete a switch or record recovery.

     start_checks explicitly arms thirty minutes of durable server checks after
    domain connection and a customer DNS change. Complete performs fresh DNS,
    trusted TLS and exact deployment response checks through the published Gregale
    edge, refuses sustained traffic budget violations and requires acknowledgement
    for limited coverage. Cancel stops checking only. confirm_restored records
    a customer attestation after manually restoring the saved provider routing,
    TTL and proxy settings. No action edits external DNS or monitor policies.
    Requires deployment write scope, verified email and MFA.

    Args:
        slug (str):
        plan (UUID):
        body (MigrationCutoverActionRequest): Explicit user action. Restored records the customer
            attestation that provider DNS was restored; Gregale never edits external DNS.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationCutover | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        plan=plan,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
    body: MigrationCutoverActionRequest,
) -> MigrationCutover | Problem | None:
    """Start checks, complete a switch or record recovery.

     start_checks explicitly arms thirty minutes of durable server checks after
    domain connection and a customer DNS change. Complete performs fresh DNS,
    trusted TLS and exact deployment response checks through the published Gregale
    edge, refuses sustained traffic budget violations and requires acknowledgement
    for limited coverage. Cancel stops checking only. confirm_restored records
    a customer attestation after manually restoring the saved provider routing,
    TTL and proxy settings. No action edits external DNS or monitor policies.
    Requires deployment write scope, verified email and MFA.

    Args:
        slug (str):
        plan (UUID):
        body (MigrationCutoverActionRequest): Explicit user action. Restored records the customer
            attestation that provider DNS was restored; Gregale never edits external DNS.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationCutover | Problem
    """

    return sync_detailed(
        slug=slug,
        plan=plan,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
    body: MigrationCutoverActionRequest,
) -> Response[MigrationCutover | Problem]:
    """Start checks, complete a switch or record recovery.

     start_checks explicitly arms thirty minutes of durable server checks after
    domain connection and a customer DNS change. Complete performs fresh DNS,
    trusted TLS and exact deployment response checks through the published Gregale
    edge, refuses sustained traffic budget violations and requires acknowledgement
    for limited coverage. Cancel stops checking only. confirm_restored records
    a customer attestation after manually restoring the saved provider routing,
    TTL and proxy settings. No action edits external DNS or monitor policies.
    Requires deployment write scope, verified email and MFA.

    Args:
        slug (str):
        plan (UUID):
        body (MigrationCutoverActionRequest): Explicit user action. Restored records the customer
            attestation that provider DNS was restored; Gregale never edits external DNS.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MigrationCutover | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        plan=plan,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    plan: UUID,
    *,
    client: AuthenticatedClient,
    body: MigrationCutoverActionRequest,
) -> MigrationCutover | Problem | None:
    """Start checks, complete a switch or record recovery.

     start_checks explicitly arms thirty minutes of durable server checks after
    domain connection and a customer DNS change. Complete performs fresh DNS,
    trusted TLS and exact deployment response checks through the published Gregale
    edge, refuses sustained traffic budget violations and requires acknowledgement
    for limited coverage. Cancel stops checking only. confirm_restored records
    a customer attestation after manually restoring the saved provider routing,
    TTL and proxy settings. No action edits external DNS or monitor policies.
    Requires deployment write scope, verified email and MFA.

    Args:
        slug (str):
        plan (UUID):
        body (MigrationCutoverActionRequest): Explicit user action. Restored records the customer
            attestation that provider DNS was restored; Gregale never edits external DNS.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MigrationCutover | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            plan=plan,
            client=client,
            body=body,
        )
    ).parsed
