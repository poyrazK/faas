// PR triage with a sandbox agent (ADR-20261027090013 §8, the acceptance
// example). The model call runs here, in the seller's process; the
// repository is checked out, read and tested in a Gregale sandbox session,
// which the run closes when it ends.
//
//   GREGALE_API_KEY=… OPENAI_API_KEY=… node dist-test/examples/pr-triage.js openai/openai-agents-js 123

import { pathToFileURL } from 'node:url';
import { run, type Model } from '@openai/agents';
import { Manifest, SandboxAgent, filesystem, shell } from '@openai/agents/sandbox';
import { GregaleSandboxClient } from '../src/index.js';

export type TriageRequest = {
  /** owner/name on GitHub. */
  repo: string;
  pr: number;
  client?: GregaleSandboxClient;
  /** A model other than the SDK's default (for tests, or another provider). */
  model?: Model | string;
};

const INSTRUCTIONS = `You triage one GitHub pull request, checked out at /workspace/repo at the PR's head.
1. Read the change: \`git -C /workspace/repo log -1 --stat\` and \`git -C /workspace/repo show --format= HEAD\`.
2. If the repository has tests you can run quickly, install what they need and run them.
3. Answer with a short report: what the change does, its risk (low, medium or high) and why,
   whether the tests passed, and the labels you would add.
Do not change files outside /workspace.`;

export function triageAgent(model?: Model | string): SandboxAgent {
  return new SandboxAgent({
    name: 'PR triage',
    instructions: INSTRUCTIONS,
    capabilities: [shell(), filesystem()],
    ...(model ? { model } : {}),
  });
}

/** Triages a pull request in a fresh Gregale session and returns the report. */
export async function triagePullRequest(req: TriageRequest): Promise<string> {
  const client = req.client ?? new GregaleSandboxClient({ network: 'registries', labels: { purpose: 'pr-triage' } });
  const manifest = new Manifest({
    entries: { repo: { type: 'git_repo', repo: req.repo, ref: `refs/pull/${req.pr}/head` } },
  });
  const result = await run(triageAgent(req.model), `Triage ${req.repo}#${req.pr}.`, { sandbox: { client, manifest } });
  return String(result.finalOutput ?? '');
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  const [repo, pr] = process.argv.slice(2);
  if (!repo || !pr) {
    console.error('usage: pr-triage <owner/name> <pull request number>');
    process.exit(2);
  }
  console.log(await triagePullRequest({ repo, pr: Number(pr) }));
}
