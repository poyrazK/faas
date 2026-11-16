"""Exercise the wheel outside the checkout, including strict result typing."""

import os
import subprocess
import sys
from pathlib import Path


def run(args, *, cwd, env=None):
    result = subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, timeout=120)
    assert result.returncode == 0, result.stdout + result.stderr
    return result


def test_wheel_exposes_async_policy_and_preserves_generated_result_types(tmp_path):
    sdk = Path(__file__).resolve().parents[1]
    dist = tmp_path / "dist"
    run([sys.executable, "-m", "build", "--wheel", "--no-isolation", "--outdir", str(dist)], cwd=sdk)
    wheels = list(dist.glob("gregale_sdk-*.whl"))
    assert len(wheels) == 1
    target = tmp_path / "installed"
    run(
        [sys.executable, "-m", "pip", "install", "--no-deps", "--no-compile", "--target", str(target), str(wheels[0])],
        cwd=tmp_path,
    )
    program = tmp_path / "app.py"
    program.write_text("""
import asyncio
import pathlib
import sys
sys.path.insert(0, sys.argv[1])
import httpx
import faas_sdk
from faas_sdk import FaaSClient, RequestOptions, ResponseInfo, RetryPolicy, PaginationOptions, PaginationPage, BuildWaitOptions, DeploymentWaitOptions, WaiterError, WaiterProgress, WaiterTimeoutError, SourceUploadOptions, SourceUploadCheckpoint, SourceUploadError, save_source_upload_checkpoint, load_source_upload_checkpoint
from faas_sdk.preview import RequestOptions as PreviewOptions
from faas_sdk.api.deployments import get_build
from faas_sdk.models.build_response import BuildResponse
assert pathlib.Path(faas_sdk.__file__).is_relative_to(pathlib.Path(sys.argv[1]))
assert PreviewOptions is RequestOptions
import faas_sdk.preview as preview
for name in ("BuildWaitOptions", "DeploymentWaitOptions", "WaiterError", "WaiterFailureCode", "WaiterOptions", "WaiterProgress", "WaiterTimeoutError", "SourceUploadCheckpoint", "SourceUploadOptions", "SourceUploadError", "save_source_upload_checkpoint", "load_source_upload_checkpoint"):
    assert getattr(preview, name) is getattr(faas_sdk, name)
def handler(request):
    value = {"id":"id","deployment_id":"deployment","kind":"tarball","source_bytes":1,"status":"succeeded","enqueued_at":"2026-11-14T00:00:00Z"}
    return httpx.Response(200, json={"items":[value], "next_before":"2026-11-14T00:00:00.123456789Z"} if request.url.path.endswith("/builds") else value)
async def exercise_sources():
    project = pathlib.Path("source-project"); project.mkdir(); (project / "index.py").write_text("print(1)")
    prepared = await faas_sdk.prepare_source(project,"prepared.tgz")
    assert prepared.plan.file_count == 1 and prepared.size > 0 and len(prepared.sha256) == 64
    assert (await faas_sdk.inspect_source(project)).file_count == 1
    assert preview.prepare_source is faas_sdk.prepare_source
    offset = 0
    receipt = None
    def transport(request):
        nonlocal offset, receipt
        path = request.url.path
        if request.method == "POST" and path == "/v1/uploads":
            return httpx.Response(201,json={"upload_id":"source","total_size":7,"chunk_size":4,"expires_at":"2026-11-16T00:00:00Z"})
        if request.method == "GET" and path == "/v1/uploads/source":
            return httpx.Response(200,json={"upload_id":"source","app_slug":"api","total_size":7,"chunk_size":4,"received_bytes":offset,"status":"open","expires_at":"2026-11-16T00:00:00Z"})
        if request.method == "PATCH":
            assert int(request.headers["Upload-Offset"]) == offset
            offset += len(request.content)
            return httpx.Response(200,headers={"Upload-Offset":str(offset)})
        if request.method == "POST":
            receipt = {"attempt_id":request.headers["Deployment-Attempt-ID"],"app_id":"3f6e0f10-c939-4e10-8e6b-15c1a39807ad","deployment_id":"4f6e0f10-c939-4e10-8e6b-15c1a39807ad","accepted_at":"2026-11-14T14:45:00Z"}
            return httpx.Response(201,json={"id":receipt["deployment_id"]})
        return httpx.Response(200,json=receipt) if receipt else httpx.Response(404,json={"code":"not_found"})
    source = pathlib.Path("archive.tar.gz"); source.write_bytes(b"archive")
    journal = pathlib.Path("source-checkpoint.json")
    client = FaaSClient(base_url="https://source.test",httpx_args={"transport":httpx.MockTransport(transport)})
    try:
        options = SourceUploadOptions(timeout=10,on_checkpoint=lambda checkpoint: save_source_upload_checkpoint(journal,checkpoint))
        checkpoint = await client.upload_source(source,"api",options=options)
        assert isinstance(checkpoint,SourceUploadCheckpoint) and checkpoint.stage == "ready" and offset == 7
        accepted = await client.commit_source(load_source_upload_checkpoint(journal),options=options)
        assert str(accepted.deployment_id) == "4f6e0f10-c939-4e10-8e6b-15c1a39807ad"
    finally:
        await client.aclose(); client.close()
async def exercise_workflow():
    total=offset=commits=0
    receipt=None
    def transport(request):
        nonlocal total,offset,commits,receipt
        path=request.url.path
        if request.method=="POST" and path=="/v1/uploads":
            import json
            total=json.loads(request.content)["total_size"]
            return httpx.Response(201,json={"upload_id":"workflow","total_size":total,"chunk_size":4096,"expires_at":"2026-11-20T00:00:00Z"})
        if request.method=="GET" and path=="/v1/uploads/workflow":
            return httpx.Response(200,json={"upload_id":"workflow","app_slug":"api","total_size":total,"chunk_size":4096,"received_bytes":offset,"status":"open","expires_at":"2026-11-20T00:00:00Z"})
        if request.method=="PATCH":
            offset+=len(request.content)
            return httpx.Response(200,headers={"Upload-Offset":str(offset)})
        if request.method=="POST":
            commits+=1
            receipt={"attempt_id":request.headers["Deployment-Attempt-ID"],"app_id":"3f6e0f10-c939-4e10-8e6b-15c1a39807ad","deployment_id":"4f6e0f10-c939-4e10-8e6b-15c1a39807ad","accepted_at":"2026-11-14T17:00:00Z"}
            return httpx.Response(201,json={"id":receipt["deployment_id"]})
        if path.endswith("/timeline"):
            return httpx.Response(200,json={"deployment_id":receipt["deployment_id"],"app":"api","status":"live","phase":"released","summary":"done","traffic_percent":100,"events":[],"created_at":"2026-11-14T17:00:00Z"})
        return httpx.Response(200,json=receipt) if receipt else httpx.Response(404,json={"code":"not_found"})
    client=FaaSClient(base_url="https://workflow.test",httpx_args={"transport":httpx.MockTransport(transport)})
    try:
        options=faas_sdk.SourceDeploymentOptions(state_path="deployment-state.json",timeout=10)
        pathlib.Path("source-project/package.json").write_text("{}")
        project=await faas_sdk.inspect_project("source-project",options=faas_sdk.ProjectInspectionOptions(app_slug="api"))
        assert not project.issues
        restored=faas_sdk.ProjectPlan.from_dict(project.to_dict())
        result=await client.deploy_project(restored,options=options)
        assert result.outcome=="succeeded" and result.timeline.phase=="released"
        resumed=await client.resume_project_deployment(project,options=options)
        assert resumed.outcome=="succeeded" and commits==1
        assert faas_sdk.load_source_deployment_state(options.state_path).upload.stage=="accepted"
        assert preview.SourceDeploymentOptions is faas_sdk.SourceDeploymentOptions
    finally:
        await client.aclose();client.close()
async def exercise_project_application():
    posts=0
    receipt=None
    def transport(request):
        nonlocal posts,receipt
        import json
        path=request.url.path
        if path.endswith('/scan'):
            return httpx.Response(200,json={"project_slug":"shop","scan_source":"single","tier":"single","workloads":[{"name":"api","root_dir":".","command":[],"ports":[8080]}],"managed":[],"crons":[],"observed_apps":0,"observed_crons":0,"limit_apps":25,"limit_crons":25,"can_apply":True,"plan_token":"reviewed"})
        if path.endswith('/apply-plan'):
            posts+=1
            attempt=request.headers["Deployment-Attempt-ID"]
            applied={"project_slug":"shop","scan_source":"single","can_apply":True,"plan_token":"reviewed","submission_attempt_id":attempt,"apps":[{"slug":"api","id":"3f6e0f10-c939-4e10-8e6b-15c1a39807ad"}],"builds":[{"slug":"api","app_id":"3f6e0f10-c939-4e10-8e6b-15c1a39807ad","deployment_id":"4f6e0f10-c939-4e10-8e6b-15c1a39807ad","build_id":"5f6e0f10-c939-4e10-8e6b-15c1a39807ad"}]}
            receipt={"attempt_id":attempt,"project_slug":"shop","selection_complete":True,"status":"complete","apply":applied}
            return httpx.Response(200,json=applied)
        if path.endswith('/timeline'):
            return httpx.Response(200,json={"deployment_id":"4f6e0f10-c939-4e10-8e6b-15c1a39807ad","app":"api","status":"live","phase":"released","summary":"test","traffic_percent":100,"events":[],"created_at":"2026-11-14T19:00:00Z"})
        return httpx.Response(200,json=receipt)
    client=FaaSClient(base_url="https://project.test",httpx_args={"transport":httpx.MockTransport(transport)})
    try:
        plan=await client.preview_project("source-project","shop",options=faas_sdk.ProjectPreviewOptions(timeout=10))
        restored=faas_sdk.ReviewedProjectPlan.from_dict(plan.to_dict())
        options=faas_sdk.ProjectApplyOptions(state_path="project-apply.json",timeout=10)
        assert (await client.apply_project_plan(restored,options=options)).outcome=="succeeded"
        assert (await client.resume_project_application(restored,options=options)).outcome=="succeeded" and posts==1
        assert faas_sdk.load_project_application_state(options.state_path).stage=="complete"
        assert preview.ProjectApplyOptions is faas_sdk.ProjectApplyOptions
    finally:
        await client.aclose()
async def main():
    await exercise_sources()
    await exercise_workflow()
    await exercise_project_application()
    reports = []
    client = FaaSClient(base_url="https://test.invalid", httpx_args={"transport":httpx.MockTransport(handler)})
    try:
        result = await client.arequest(lambda: get_build.asyncio("id", client=client.inner), options=RequestOptions(timeout=1, retry=RetryPolicy(1), on_response=reports.append))
        assert isinstance(result, BuildResponse) and result.id == "id"
        assert len(reports) == 1 and isinstance(reports[0], ResponseInfo)
        assert get_build.sync("id", client=client.inner).id == "id"
        progress = []
        waited = await client.wait_for_build("id", options=BuildWaitOptions(timeout=1, on_progress=lambda value, info: progress.append(info)))
        assert isinstance(waited, BuildResponse) and waited.id == "id"
        assert isinstance(progress[0], WaiterProgress)
        async with client.aiter_build_pages(options=PaginationOptions(max_pages=1)) as pages:
            page = await anext(pages)
            assert isinstance(page, PaginationPage) and isinstance(page.items[0], BuildResponse)
            assert page.next_before == "2026-11-14T00:00:00.123456789Z"
    finally:
        await client.aclose()
        client.close()
asyncio.run(main())
""")
    run([sys.executable, "-I", str(program), str(target)], cwd=tmp_path)
    positive = tmp_path / "typed.py"
    positive.write_text("""
from __future__ import annotations
from typing import assert_type
from faas_sdk import JobContract,JobInput,JobSubmitOptions,JobRunOptions,JobProgress,JobItemOutcome,TypedJobRun
from faas_sdk.preview import LiveJob,LiveJobSnapshot,LiveJobFailure,LiveJobSource,realtime_live_job_source
from faas_sdk.preview import PipelineChild,PipelineChildOptions,PipelineBatchResult,PipelineChildView,PipelineGateOptions,PipelineGateOutcome,PipelineGateService,JobPipeline,JobPipelineClient,PipelineCheckpoint,PipelineInspection,TypedPipelineRun,JobPipelineContext,PipelineJob,PipelineJobResult
async def typed_job_pipeline(client:JobPipelineClient[str,int],context:JobPipelineContext,job:PipelineJob[str,int])->None:
    run=await client.start("one",timeout=1,idempotency_key="one")
    assert_type(run,TypedPipelineRun[int])
    assert_type(run.checkpoint,PipelineCheckpoint)
    assert_type(await run.result(timeout=1),int)
    assert_type(await run.inspect(timeout=1),PipelineInspection)
    child:PipelineChild[str,int]=PipelineChild(contract=job.contract)
    assert_type(await context.child("child",child,"input",options=PipelineChildOptions(timeout=60)),PipelineChildView[int])
    assert_type(await context.children("children",child,[JobInput("one","input")],options=PipelineChildOptions(timeout=60,concurrency=2)),PipelineBatchResult[int])
    assert_type(await context.approval("review",job.contract,"input",options=PipelineGateOptions(timeout=60,required_roles=("reviewer",))),PipelineGateOutcome[int])
    assert_type(await context.wait_for_input("reply",job.contract,"input",options=PipelineGateOptions(timeout=60,required_roles=("reviewer",))),PipelineGateOutcome[int])
    assert_type(await context.job("work",[JobInput("one","input")],definition=job),PipelineJobResult[int])
async def typed_live_jobs(live: LiveJob) -> None:
    async for snapshot in live.updates():
        assert_type(snapshot,LiveJobSnapshot)
        assert_type(snapshot.failures,tuple[LiveJobFailure,...])
        assert_type(snapshot.complete,bool)
async def typed_jobs(client: FaaSClient) -> None:
    contract:JobContract[str,int]=JobContract({"version":1,"name":"typed-job","input":{"type":"string"},"output":{"type":"integer"}})
    run=await client.define_job(contract).submit([JobInput("one","input")],options=JobSubmitOptions(timeout=1,idempotency_key="one"))
    assert_type(run,TypedJobRun[str,int])
    assert_type(await run.wait(options=JobRunOptions(timeout=1)),JobProgress)
    assert_type(await run.results(options=JobRunOptions(timeout=1)),tuple[JobItemOutcome[int],...])
from faas_sdk import FaaSClient, RequestOptions, RetryPolicy, ResponseInfo, AsyncPagination, PaginationOptions, PaginationPage, BuildWaitOptions, DeploymentWaitOptions, WaiterOptions, WaiterError, WaiterFailureCode, WaiterProgress, WaiterTimeoutError
from faas_sdk.api.deployments import get_build
from faas_sdk.models.build_response import BuildResponse
from faas_sdk.models.problem import Problem
from faas_sdk.types import Response
from faas_sdk.models.deployment_response import DeploymentResponse
from faas_sdk.models.release_timeline import ReleaseTimeline
from faas_sdk.models.deployment_submission_receipt import DeploymentSubmissionReceipt
from faas_sdk import SourceUploadOptions, SourceUploadCheckpoint, SourceUploadError, SourceUploadProgress, SourceUploadCode
from faas_sdk.api.uploads import append_upload
from faas_sdk.api.deployments import list_deployments
from faas_sdk.preview import FaaSClient as PreviewClient, PaginationOptions as PreviewPaginationOptions, BuildWaitOptions as PreviewBuildWaitOptions
import datetime
from faas_sdk import prepare_source, inspect_source, PreparedSource, SourcePlan, SourcePreparationOptions, SourcePreparationProgress
async def check_preparation() -> None:
    assert_type(await inspect_source("project"),SourcePlan)
    assert_type(await prepare_source("project","archive.tgz",options=SourcePreparationOptions(source_root="apps/api")),PreparedSource)
from faas_sdk import SourceDeploymentOptions,SourceDeploymentResult,SourceDeploymentError,SourceDeploymentState,SourceDeploymentPhase,SourceDeploymentProgress,ProjectPlan,ProjectInspectionOptions,inspect_project,validate_project_plan
from faas_sdk import ProjectPreviewOptions,ProjectApplyOptions,ReviewedProjectPlan,ProjectApplicationResult,ProjectWorkloadOutcome,ProjectWorkflowError,ProjectVerificationOptions,ProjectVerificationTarget,ProjectVerificationReport,VerificationWorkloadReport,project_verification_target
from faas_sdk.models.plan_response import PlanResponse
from faas_sdk.api.projects import apply_cached_project_plan
from faas_sdk.models.apply_cached_project_plan_request import ApplyCachedProjectPlanRequest
async def check_project_application(client: FaaSClient, preview: PreviewClient) -> None:
    plan=await client.preview_project("project","shop",options=ProjectPreviewOptions(timeout=1))
    assert_type(plan,ReviewedProjectPlan)
    assert_type(plan.preview,PlanResponse)
    options=ProjectApplyOptions(state_path="../apply.json",timeout=1)
    result=await client.apply_project_plan(plan,options=options)
    assert_type(result,ProjectApplicationResult)
    assert_type(result.workloads,tuple[ProjectWorkloadOutcome,...])
    target=project_verification_target(result)
    assert_type(target,ProjectVerificationTarget)
    verification=await client.verify_project_application(target,options=ProjectVerificationOptions(timeout=1))
    assert_type(verification,ProjectVerificationReport)
    assert_type(verification.workloads,tuple[VerificationWorkloadReport,...])
    assert_type(await preview.verify_project_application(target,options=ProjectVerificationOptions(timeout=1)),ProjectVerificationReport)
    assert_type(await preview.resume_project_application(plan,options=options),ProjectApplicationResult)
    await apply_cached_project_plan.asyncio(client=client.inner,body=ApplyCachedProjectPlanRequest(plan_token="reviewed"),deployment_attempt_id="a"*32)
async def check_project(client: FaaSClient, preview: PreviewClient) -> None:
    plan=await inspect_project("project",options=ProjectInspectionOptions(app_slug="api"))
    assert_type(plan,ProjectPlan)
    assert_type(plan.runtime,str | None)
    await validate_project_plan(plan)
    options=SourceDeploymentOptions(state_path="../project.json",timeout=1)
    assert_type(await client.deploy_project(plan,options=options),SourceDeploymentResult)
    assert_type(await preview.resume_project_deployment(ProjectPlan.from_dict(plan.to_dict()),options=options),SourceDeploymentResult)
async def check_workflow(client: FaaSClient) -> None:
    options=SourceDeploymentOptions(state_path="../deploy.json",timeout=1)
    result=await client.deploy_source("project","api",options=options)
    assert_type(result,SourceDeploymentResult)
    assert_type(result.timeline,ReleaseTimeline)
    assert_type(await client.resume_source_deployment("api",options=options),SourceDeploymentResult)
def inspect_workflow_error(error: SourceDeploymentError, event: SourceDeploymentProgress) -> None:
    assert_type(error.state,SourceDeploymentState | None)
    assert_type(event.phase,SourceDeploymentPhase)
async def check(client: FaaSClient) -> None:
    policy = SourceUploadOptions(timeout=30, on_checkpoint=save_source, on_progress=source_progress)
    checkpoint = await client.upload_source("archive.tar.gz", "api", options=policy)
    assert_type(checkpoint, SourceUploadCheckpoint)
    assert_type(checkpoint.received_bytes, int)
    accepted = await client.commit_source(checkpoint, options=policy)
    assert_type(accepted, DeploymentSubmissionReceipt)
    appended = await append_upload.asyncio_detailed("id",client=client.inner,body=b"bytes",upload_offset=0)
    assert_type(appended.headers["Upload-Offset"], str)
    value = await client.arequest(lambda: get_build.asyncio("id", client=client.inner), options=RequestOptions(timeout=1, retry=RetryPolicy(2)))
    assert_type(value, BuildResponse | Problem | None)
    assert_type(await client.wait_for_build("id", options=BuildWaitOptions(timeout=1)), BuildResponse)
    assert_type(await client.wait_for_deployment("id", options=DeploymentWaitOptions(timeout=1)), ReleaseTimeline)
    typed_policy: WaiterOptions[BuildResponse] = BuildWaitOptions(timeout=1, on_progress=observe_build)
    assert_type(typed_policy.timeout, float)
    detailed = await client.arequest(lambda: get_build.asyncio_detailed("id", client=client.inner))
    assert_type(detailed, Response[BuildResponse | Problem])
    assert_type(client.aiter_builds(status="running"), AsyncPagination[BuildResponse])
    assert_type(client.aiter_deployments(), AsyncPagination[DeploymentResponse])
    assert_type(client.aiter_app_deployments("api"), AsyncPagination[DeploymentResponse])
    assert_type(client.aiter_deployment_pages(), AsyncPagination[PaginationPage[DeploymentResponse]])
    assert_type(client.aiter_app_deployment_pages("api"), AsyncPagination[PaginationPage[DeploymentResponse]])
    async with client.aiter_build_pages(options=PaginationOptions(max_items=2)) as pages:
        page = await anext(pages)
        assert_type(page, PaginationPage[BuildResponse])
        assert_type(page.items, tuple[BuildResponse, ...])
        assert_type(page.next_before, str | None)
    await list_deployments.asyncio(client=client.inner, before=datetime.datetime.now())
    await list_deployments.asyncio(client=client.inner, before="2026-11-14T00:00:00.123456789Z")
async def preview(client: PreviewClient) -> None:
    assert_type(client.aiter_builds(options=PreviewPaginationOptions(max_pages=1)), AsyncPagination[BuildResponse])
    assert_type(await client.wait_for_build("id", options=BuildWaitOptions(timeout=1)), BuildResponse)
def save_source(checkpoint: SourceUploadCheckpoint) -> None:
    assert_type(checkpoint.sha256_hex, str)
def source_progress(info: SourceUploadProgress) -> None:
    assert_type(info.received_bytes, int)
def inspect_source_error(error: SourceUploadError) -> None:
    assert_type(error.code, SourceUploadCode)
    assert_type(error.checkpoint, SourceUploadCheckpoint | None)
async def observe_build(value: BuildResponse, info: WaiterProgress) -> None:
    assert_type(value, BuildResponse)
    assert_type(info.poll, int)
    assert_type(info.elapsed_ms, float)
def inspect_stop(error: WaiterError) -> None:
    assert_type(error.code, WaiterFailureCode)
    assert_type(error.last_observed, BuildResponse | ReleaseTimeline | None)
    assert_type(error.polls, int)
def observe(info: ResponseInfo) -> None:
    assert_type(info.request_id, str | None)
    assert_type(info.duration_ms, float)
""")
    env = {**os.environ, "MYPYPATH": str(target)}
    args = [
        sys.executable,
        "-m",
        "mypy",
        "--strict",
        "--python-version",
        "3.11",
        "--follow-imports=silent",
        "--ignore-missing-imports",
    ]
    run([*args, str(positive)], cwd=tmp_path, env=env)
    negative = tmp_path / "invalid.py"
    negative.write_text("""
from faas_sdk import RequestOptions, ResponseInfo, RetryPolicy, FaaSClient, PaginationOptions, PaginationPage, BuildWaitOptions, DeploymentWaitOptions, WaiterProgress, SourceUploadCheckpoint, SourceUploadOptions
def check(options: RequestOptions, info: ResponseInfo) -> None:
    options.timeout = 2
    info.status = 200
    RetryPolicy(max_attempts="two")
def pagination(client: FaaSClient, page: PaginationPage[int]) -> None:
    page.next_before = "changed"
    PaginationOptions(max_items="three")
    client.aiter_builds(status="cancelled")
def invalid_waiters(client: FaaSClient, info: WaiterProgress) -> None:
    BuildWaitOptions()
    BuildWaitOptions(timeout=1, max_polls="two")
    client.wait_for_build("id", options=DeploymentWaitOptions(timeout=1))
    info.poll = 2
def invalid_sources(checkpoint: SourceUploadCheckpoint) -> None:
    SourceUploadOptions(timeout=1)
    checkpoint.received_bytes = 2
""")
    result = subprocess.run([*args, str(negative)], cwd=tmp_path, env=env, text=True, capture_output=True, timeout=120)
    assert result.returncode == 1
    assert result.stdout.count("read-only") == 5
    assert '"max_attempts"' in result.stdout
    assert '"max_items"' in result.stdout
    assert '"status"' in result.stdout
    assert '"max_polls"' in result.stdout
    assert '"timeout"' in result.stdout
    assert '"options"' in result.stdout
    assert '"on_checkpoint"' in result.stdout
