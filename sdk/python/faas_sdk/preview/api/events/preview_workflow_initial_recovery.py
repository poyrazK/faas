from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_initial_recovery_preview import WorkflowInitialRecoveryPreview
from ...types import Response


def _get_kwargs(
    slug: str,
    failure_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflow-failures/{failure_id}/recovery-preview".format(
            slug=quote(str(slug), safe=""),
            failure_id=quote(str(failure_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowInitialRecoveryPreview | None:
    if response.status_code == 200:
        response_200 = WorkflowInitialRecoveryPreview.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowInitialRecoveryPreview]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    failure_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | WorkflowInitialRecoveryPreview]:
    """Review first recovery eligibility for a captured failure

     Read-only consistent live inspection of retained generation-zero evidence, current generation, safe
    resume diagnostics and operation ownership. Returns a generation-fenced target only when eligible.
    Cached later eligibility and policy existence do not authorize recovery. This does not prepare a
    policy review or admit a run. Operators may submit the exact reviewed target through event recovery
    creation with a stable request_id; creation and worker admission recheck evidence retention and
    generation, and resume rechecks safety and capacity. No automatic first recovery is enabled.

    Args:
        slug (str):
        failure_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialRecoveryPreview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        failure_id=failure_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    failure_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowInitialRecoveryPreview | None:
    """Review first recovery eligibility for a captured failure

     Read-only consistent live inspection of retained generation-zero evidence, current generation, safe
    resume diagnostics and operation ownership. Returns a generation-fenced target only when eligible.
    Cached later eligibility and policy existence do not authorize recovery. This does not prepare a
    policy review or admit a run. Operators may submit the exact reviewed target through event recovery
    creation with a stable request_id; creation and worker admission recheck evidence retention and
    generation, and resume rechecks safety and capacity. No automatic first recovery is enabled.

    Args:
        slug (str):
        failure_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialRecoveryPreview
    """

    return sync_detailed(
        slug=slug,
        failure_id=failure_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    failure_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Problem | WorkflowInitialRecoveryPreview]:
    """Review first recovery eligibility for a captured failure

     Read-only consistent live inspection of retained generation-zero evidence, current generation, safe
    resume diagnostics and operation ownership. Returns a generation-fenced target only when eligible.
    Cached later eligibility and policy existence do not authorize recovery. This does not prepare a
    policy review or admit a run. Operators may submit the exact reviewed target through event recovery
    creation with a stable request_id; creation and worker admission recheck evidence retention and
    generation, and resume rechecks safety and capacity. No automatic first recovery is enabled.

    Args:
        slug (str):
        failure_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowInitialRecoveryPreview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        failure_id=failure_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    failure_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Problem | WorkflowInitialRecoveryPreview | None:
    """Review first recovery eligibility for a captured failure

     Read-only consistent live inspection of retained generation-zero evidence, current generation, safe
    resume diagnostics and operation ownership. Returns a generation-fenced target only when eligible.
    Cached later eligibility and policy existence do not authorize recovery. This does not prepare a
    policy review or admit a run. Operators may submit the exact reviewed target through event recovery
    creation with a stable request_id; creation and worker admission recheck evidence retention and
    generation, and resume rechecks safety and capacity. No automatic first recovery is enabled.

    Args:
        slug (str):
        failure_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowInitialRecoveryPreview
    """

    return (
        await asyncio_detailed(
            slug=slug,
            failure_id=failure_id,
            client=client,
        )
    ).parsed
