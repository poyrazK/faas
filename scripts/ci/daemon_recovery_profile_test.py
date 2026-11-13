import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from daemon_recovery_profile import validate


class RecoveryProfileTests(unittest.TestCase):
    def fixture(self):
        temporary = tempfile.TemporaryDirectory(prefix='daemon-recovery-policy-')
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        def git(*args):
            return subprocess.check_output(['git', '-C', str(root), *args], text=True, stderr=subprocess.DEVNULL).strip()
        git('init', '-q')
        git('config', 'user.name', 'Policy fixture')
        git('config', 'user.email', 'fixture@example.invalid')
        git('config', 'commit.gpgsign', 'false')
        (root / 'go.mod').write_text('module fixture\n')
        (root / 'guest').mkdir()
        (root / 'guest/init.go').write_text('package main\n')
        git('add', '.')
        git('commit', '-qm', 'base')
        base = git('rev-parse', 'HEAD')
        git('tag', 'v0.1.18-rc.341')
        (root / '.release').mkdir()
        profile = root / '.release/daemon-recovery.json'
        profile.write_text(json.dumps({'mode': 'daemon-recovery', 'base_release': 'v0.1.18-rc.341', 'base_sha': base}))
        return root, git, profile

    def commit(self, git):
        git('add', '.')
        git('commit', '-qm', 'candidate')

    def test_ordinary_release_keeps_client_publication(self):
        root, git, profile = self.fixture()
        profile.unlink()
        self.assertTrue(validate(root))

    def test_reviewed_adoption_repair_preserves_channels(self):
        root, git, _ = self.fixture()
        path = root / 'pkg/fcvm/vmm_adopt.go'
        path.parent.mkdir(parents=True)
        path.write_text('package fcvm\n')
        self.commit(git)
        self.assertFalse(validate(root))

    def test_runtime_api_dependency_and_schema_changes_refuse_recovery(self):
        for name in ['guest/init.go', 'pkg/api/limits.go', 'go.mod', 'images/Dockerfile', 'migrations/extra.sql']:
            with self.subTest(path=name):
                root, git, _ = self.fixture()
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('changed\n')
                self.commit(git)
                with self.assertRaisesRegex(ValueError, 'outside the reviewed adoption repair'):
                    validate(root)

    def test_renaming_guest_code_cannot_hide_a_runtime_change(self):
        root, git, _ = self.fixture()
        path = root / 'pkg/fcvm/vmm_adopt.go'
        path.parent.mkdir(parents=True)
        (root / 'guest/init.go').rename(path)
        self.commit(git)
        with self.assertRaisesRegex(ValueError, 'outside the reviewed adoption repair'):
            validate(root)

    def test_unknown_profile_or_wrong_base_fails_closed(self):
        root, git, profile = self.fixture()
        data = json.loads(profile.read_text())
        data['unexpected'] = True
        profile.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, 'invalid daemon recovery profile'):
            validate(root)
        del data['unexpected']
        data['base_sha'] = '0' * 40
        profile.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, 'does not match'):
            validate(root)


if __name__ == '__main__':
    unittest.main()
