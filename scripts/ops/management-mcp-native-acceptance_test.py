import copy
import importlib.util
import json
import os
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('native', Path(__file__).with_name('management-mcp-native-acceptance.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

class AcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.config = json.loads(Path(__file__).with_name('fixtures').joinpath('management-mcp/worker.json').read_text())

    def test_native_source_uses_workload_apply(self):
        for mode in ('service', 'worker'):
            scenario = json.loads(Path(__file__).with_name('fixtures').joinpath('management-mcp/' + mode + '-source.json').read_text())
            m.validate(scenario)
            scenario['steps'][1]['tool'] = 'gregale_apply_deploy'
            with self.assertRaises(ValueError):
                m.validate(scenario)

    def test_wrong_target_never_writes(self):
        calls = []
        def call(tool, args):
            calls.append(tool)
            return {'account_id': 'other'}
        with self.assertRaises(RuntimeError):
            m.run(self.config, call)
        self.assertEqual(calls, ['gregale_plan_workload_rollout'])

    def test_verification_is_read_only_and_references_a_reviewed_plan(self):
        scenario = copy.deepcopy(self.config)
        scenario['steps'].append({'id': 'verify', 'tool': 'gregale_verify_plan',
                                 'arguments': {'plan_id': {'ref': 'plan.plan_id'}},
                                 'poll': True, 'expect': {'verification.status': 'verified'}})
        m.validate(scenario)
        for reference in ('apply.plan_id', 'plan.deployment.id'):
            invalid = copy.deepcopy(scenario)
            invalid['steps'][-1]['arguments']['plan_id']['ref'] = reference
            with self.assertRaises(ValueError):
                m.validate(invalid)

    def test_replay_and_convergence(self):
        plan = dict(self.config['target'], account_id=self.config['account_id'], plan_id='p', rollout_mode='worker')
        calls = []
        def call(tool, args):
            calls.append((tool, copy.deepcopy(args)))
            if tool in m.PLANS:
                return plan
            if tool in m.WRITES:
                return {'deployment': {'id': 'd', 'app_id': plan['app_id']}}
            return {'worker_observation': {'generation_converged': True, 'queue_processing_confirmed': False}}
        evidence = m.run(self.config, call)
        self.assertEqual(len(evidence), 3)
        self.assertEqual(calls[1], calls[2])
        self.assertEqual(calls[3][1]['deployment_id'], 'd')

    def test_rejects_unreviewed_writes_and_cross_scope(self):
        for mutation in ('plan', 'scope', 'poll'):
            c = copy.deepcopy(self.config)
            if mutation == 'plan':
                c['steps'][1]['arguments']['plan_id'] = 'unreviewed'
            elif mutation == 'scope':
                c['steps'][0]['arguments']['environment'] = 'production'
            else:
                c['steps'][1]['poll'] = True
            with self.assertRaises(ValueError):
                m.validate(c)

    def test_changed_replay_fails(self):
        c = copy.deepcopy(self.config)
        c['steps'] = c['steps'][:2]
        count = 0
        def call(tool, args):
            nonlocal count
            if tool in m.PLANS:
                return dict(c['target'], account_id=c['account_id'], plan_id='p', rollout_mode='worker')
            count += 1
            return {'deployment': {'id': str(count), 'app_id': c['target']['app_id']}}
        with self.assertRaises(RuntimeError):
            m.run(c, call)

class ProtectedRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.config = json.loads(Path(__file__).with_name('fixtures').joinpath('management-mcp/protected-recovery.json').read_text())

    def test_checkpoints_keep_original_plan_and_receipt(self):
        actions, writes = [], []
        target = self.config['environment_target']
        plan = {key: value for key, value in target.items() if key != 'review_origin'}
        plan.update(account_id=self.config['account_id'], approval_required=True, plan_id='original')
        def call(tool, args):
            if tool == m.ENV_PLAN:
                return plan
            if tool == 'gregale_get_environment_promotion':
                return {'promotion_id': 'promotion-original', 'status': 'completed'}
            writes.append(copy.deepcopy(args))
            if 'human_review' not in actions:
                return {'status': 'approval_pending', 'review_url': 'https://console.example/cli-auth?approval=public-ticket'}
            return {'promotion_id': 'promotion-original', 'project_slug': 'acceptance', 'from_environment': 'staging', 'to_environment': 'protected-test'}
        def checkpoint(action, value):
            if action == 'human_review':
                self.assertEqual(value['status'], 'approval_pending')
            actions.append(action)
        report = m.run(self.config, call, checkpoint)
        self.assertEqual(actions, ['restart_bridge', 'human_review', 'restart_bridge'])
        self.assertTrue(all(args == {'plan_id': 'original'} for args in writes))
        self.assertNotIn('public-ticket', json.dumps(report))
        self.assertEqual(report[2]['status'], 'operator_acknowledged')

    def test_changed_workload_list_or_environment_never_writes(self):
        for key in ('changes', 'from_environment_id', 'environment_id', 'account_id'):
            calls = []
            def call(tool, args):
                calls.append(tool)
                target = self.config['environment_target']
                plan = dict(target, account_id=self.config['account_id'], approval_required=True)
                plan[key] = [] if key == 'changes' else 'recreated'
                return plan
            with self.assertRaises(RuntimeError):
                m.run(self.config, call)
            self.assertEqual(calls, [m.ENV_PLAN])

    def test_approval_credentials_cannot_be_arguments(self):
        self.config['steps'][1]['arguments']['approval_token'] = 'private'
        with self.assertRaises(ValueError):
            m.validate(self.config)

    def test_review_url_requires_exact_console(self):
        origin = 'https://console.example'
        good = origin + '/cli-auth?approval=ticket'
        self.assertEqual(m.checked_review_url({'status':'approval_pending','review_url':good},origin),good)
        for bad in ('https://attacker.example/cli-auth?approval=ticket', good+'\n', good+'&extra=value', origin+'/other?approval=ticket', good+'&approval=other'):
            with self.assertRaises(RuntimeError):
                m.checked_review_url({'status':'approval_pending','review_url':bad},origin)

    def test_rejected_errors_only_keep_code(self):
        result = {'isError':True,'structuredContent':{'code':'approval_unavailable','detail':'private-token'}}
        self.assertEqual(m.decode_result(result), {'mcp_error_code':'approval_unavailable'})

    def test_missing_operator_adapter_stops_before_resume(self):
        target = self.config['environment_target']
        def call(tool,args):
            if tool == m.ENV_PLAN:
                return dict(target,account_id=self.config['account_id'],approval_required=True,plan_id='p')
            return {'status':'approval_pending'}
        with self.assertRaises(RuntimeError):
            m.run(self.config,call)


class OutputReaderTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location(
            'metrics', Path(__file__).with_name('management-mcp-metrics-acceptance.py'))
        self.metrics = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.metrics)

    def pipe(self, binary=False):
        reader, writer = os.pipe()
        stream = os.fdopen(reader, 'rb') if binary else os.fdopen(reader, 'r', encoding='utf-8')
        output = os.fdopen(writer, 'wb', buffering=0)
        self.addCleanup(stream.close)
        self.addCleanup(output.close)
        return stream, output

    def test_coalesced_notices_are_read_without_waiting_for_more_output(self):
        for binary in (False, True):
            with self.subTest(binary=binary):
                stream, output = self.pipe(binary)
                output.write(b'metrics listening\nMCP listening\n')
                for expected in (b'metrics listening\n', b'MCP listening\n'):
                    self.assertEqual(self.metrics.read_line(stream, timeout=0.5),
                                     expected if binary else expected.decode())

    def test_partial_line_has_a_deadline(self):
        stream, output = self.pipe()
        output.write(b'partial notice')
        with self.assertRaisesRegex(RuntimeError, 'CLI output timed out'):
            self.metrics.read_line(stream, timeout=0.02)

    def test_final_partial_line_and_eof(self):
        stream, output = self.pipe()
        output.write(b'last notice')
        output.close()
        self.assertEqual(self.metrics.read_line(stream, timeout=0.5), 'last notice')
        with self.assertRaisesRegex(RuntimeError, 'CLI ended early'):
            self.metrics.read_line(stream, timeout=0.5)


if __name__ == '__main__':
    unittest.main()
