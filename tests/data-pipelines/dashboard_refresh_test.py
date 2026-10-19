"""Verify that progress refresh cannot discard an active customer's form."""
import pathlib
import re
import subprocess
import unittest


class DashboardRefreshTests(unittest.TestCase):
    def test_refresh_waits_for_idle_forms(self):
        template = (pathlib.Path(__file__).resolve().parents[2] / "pkg/dashboard/templates/data_pipelines.html").read_text()
        self.assertNotIn('http-equiv="refresh"', template)
        script = re.search(r'<script nonce="\{\{\.Nonce\}\}">(.*?)</script>', template, re.S).group(1)
        script = script.replace("{{.Data.RefreshSeconds}}", "2")
        harness = """
const vm = require('node:vm');
const assert = require('node:assert/strict');
const script = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
function page() {
 let reloads=0, tick; const events={};
 const document={visibilityState:'visible', activeElement:null, addEventListener:(kind, fn)=>events[kind]=fn};
 vm.runInNewContext(script,{document, window:{location:{reload:()=>reloads++}}, setInterval:(fn)=>tick=fn});
 return {document,events,tick, count:()=>reloads};
}
const idle=page(); idle.tick(); assert.equal(idle.count(),1);
const focused=page(); focused.document.activeElement={closest:()=>true}; focused.tick(); assert.equal(focused.count(),0);
for (const kind of ['input','change']) {
 const edited=page(); edited.events[kind]({target:{closest:()=>true}}); edited.document.activeElement=null;
 edited.tick(); assert.equal(edited.count(),0);
}
const hidden=page(); hidden.document.visibilityState='hidden'; hidden.tick(); assert.equal(hidden.count(),0);
"""
        import json
        result = subprocess.run(["node", "-e", harness], input=json.dumps(script), text=True, capture_output=True, timeout=10, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
