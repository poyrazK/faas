from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.scenario_suspend_resume_evidence import ScenarioSuspendResumeEvidence
from ...types import Response


def _get_kwargs(
    run_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/test-runs/{run_id}/suspend-resume".format(
            run_id=quote(str(run_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScenarioSuspendResumeEvidence | None:
    if response.status_code == 200:
        response_200 = ScenarioSuspendResumeEvidence.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScenarioSuspendResumeEvidence]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ScenarioSuspendResumeEvidence]:
    """Request the enrolled after-setup suspend/resume checkpoint.

     Account-owned isolated run only. Managed-job checkpoints arm automatically through their original
    relay and return 409 here. After-setup requests are idempotent and do not repeat uncertain pauses.
    Complete requires original VM and collector receipts plus scheduler publication. Durable handling
    remains available when new enrollment is disabled.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioSuspendResumeEvidence]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ScenarioSuspendResumeEvidence | None:
    """Request the enrolled after-setup suspend/resume checkpoint.

     Account-owned isolated run only. Managed-job checkpoints arm automatically through their original
    relay and return 409 here. After-setup requests are idempotent and do not repeat uncertain pauses.
    Complete requires original VM and collector receipts plus scheduler publication. Durable handling
    remains available when new enrollment is disabled.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioSuspendResumeEvidence
    """

    return sync_detailed(
        run_id=run_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ScenarioSuspendResumeEvidence]:
    """Request the enrolled after-setup suspend/resume checkpoint.

     Account-owned isolated run only. Managed-job checkpoints arm automatically through their original
    relay and return 409 here. After-setup requests are idempotent and do not repeat uncertain pauses.
    Complete requires original VM and collector receipts plus scheduler publication. Durable handling
    remains available when new enrollment is disabled.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScenarioSuspendResumeEvidence]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ScenarioSuspendResumeEvidence | None:
    """Request the enrolled after-setup suspend/resume checkpoint.

     Account-owned isolated run only. Managed-job checkpoints arm automatically through their original
    relay and return 409 here. After-setup requests are idempotent and do not repeat uncertain pauses.
    Complete requires original VM and collector receipts plus scheduler publication. Durable handling
    remains available when new enrollment is disabled.

    Args:
        run_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScenarioSuspendResumeEvidence
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
        )
    ).parsed
