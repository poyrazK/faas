"""Validate a maintenance release that preserves public client channels."""
import json
from pathlib import Path
import re
import subprocess

ALLOWED = {
    '.release/daemon-recovery.json',
    '.github/workflows/release.yml',
    '.github/workflows/ci-light.yml',
    'scripts/ci/daemon_recovery_profile.py',
    'scripts/ci/daemon_recovery_profile_test.py',
    'docs/adr/20261113025000-daemon-recovery-release.md',
    'docs/adr/20261017-vmmd-restart-adoption.md',
    'pkg/fcvm/adoption_experiments_metal_test.go',
    'pkg/fcvm/vmm.go',
    'pkg/fcvm/vmm_adopt.go',
    'pkg/fcvm/vmm_adopt_linux_test.go',
    'pkg/fcvm/vmm_adopt_bind_linux_test.go',
}


def validate(root):
    profile = root / '.release/daemon-recovery.json'
    if not profile.exists():
        return True
    data = json.loads(profile.read_text())
    if set(data) != {'mode', 'base_release', 'base_sha'} or data['mode'] != 'daemon-recovery':
        raise ValueError('invalid daemon recovery profile')
    if not re.fullmatch(r'v0\.\d+\.\d+-rc\.\d+', data['base_release']):
        raise ValueError('recovery base must be a prerelease tag')
    if not re.fullmatch(r'[0-9a-f]{40}', data['base_sha']):
        raise ValueError('recovery base must name an exact commit')
    def git(*args):
        return subprocess.check_output(['git', '-C', str(root), *args], text=True, timeout=30).strip()
    if git('rev-parse', data['base_release'] + '^{commit}') != data['base_sha']:
        raise ValueError('recovery base tag does not match its pinned commit')
    subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor', data['base_sha'], 'HEAD'], check=True, timeout=30)
    paths = set(git('diff', '--no-renames', '--name-only', data['base_sha'], 'HEAD').splitlines())
    if paths - ALLOWED:
        raise ValueError('recovery changes inputs outside the reviewed adoption repair: ' + ', '.join(sorted(paths - ALLOWED)))
    return False


if __name__ == '__main__':
    publish = validate(Path(__file__).resolve().parents[2])
    print('publish_clients=' + str(publish).lower())
