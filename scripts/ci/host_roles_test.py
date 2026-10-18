#!/usr/bin/env python3
"""Unit tests for host_roles.py (per-role content hashes, ADR 20261018133100)."""
import importlib.util
import pathlib
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
spec = importlib.util.spec_from_file_location("host_roles", HERE / "host_roles.py")
hr = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hr)

BOOTSTRAP = """\
# Shared discovery play.
- name: Discover addresses
  hosts: control_plane:compute_nodes
  tasks:
    - name: probe
      ansible.builtin.debug: {}

- name: Bootstrap the control plane
  hosts: control_plane
  pre_tasks:
    - name: gather
      ansible.builtin.setup:
  roles:
    # Shared preconditions.
    - role: nftables
    - role: postgres
      when: faas_postgres_enabled | default(true) | bool
    - role: prometheus
    - role: control_plane_service
    - role: s3_gateway_service
    - role: gatewayd_public_service

- name: Bootstrap compute
  hosts: compute_nodes
  roles:
    - role: nftables
    - role: vmmd_service
"""

FILES = {
    "deploy/ansible/bootstrap.yml": BOOTSTRAP,
    "deploy/ansible/ansible.cfg": "[defaults]\nforks = 1\n",
    "deploy/ansible/requirements.yml": "collections: []\n",
    "deploy/ansible/control_plane_converge.yml": "- import_playbook: bootstrap.yml\n",
    "deploy/ansible/node_join.yml": "- import_playbook: bootstrap.yml\n",
    "deploy/ansible/group_vars/control_plane/backup.yml": "faas_backup: true\n",
    "deploy/ansible/group_vars/compute_nodes/log.yml": "faas_log: true\n",
    "deploy/ansible/tasks/validate_udp_policy.yml": "- ansible.builtin.assert: {that: [true]}\n",
    "deploy/ansible/roles/_shared/templates/99-node-name.conf.j2": "[Service]\n",
    "deploy/ansible/roles/_shared/README.md": "shared role\n",
    "deploy/ansible/roles/nftables/tasks/main.yml":
        "- ansible.builtin.include_tasks: \"{{ role_path }}/../../tasks/validate_udp_policy.yml\"\n"
        "- ansible.builtin.template: {src: rules.nft.j2, dest: /etc/nftables.conf}\n",
    "deploy/ansible/roles/nftables/templates/rules.nft.j2": "table inet faas {}\n",
    "deploy/ansible/roles/postgres/tasks/main.yml":
        "# Install the backup units from the shared unit tree: {{ role_path }}/../../../nowhere\n"
        "- ansible.builtin.copy:\n    src: \"{{ role_path }}/../../../systemd/faas-pg-basebackup.service\"\n"
        "    dest: /etc/systemd/system/faas-pg-basebackup.service\n",
    "deploy/ansible/roles/postgres/defaults/main.yml": "postgres_max_connections: 170\n",
    "deploy/ansible/roles/postgres/README.md": "postgres role\n",
    "deploy/ansible/roles/prometheus/tasks/main.yml":
        "- ansible.builtin.copy: {src: faas.rules.yml, dest: /etc/prometheus/faas.rules.yml}\n"
        "  notify: reload prometheus\n",
    "deploy/ansible/roles/prometheus/handlers/main.yml":
        "- name: reload prometheus\n  ansible.builtin.systemd_service: {name: prometheus, state: reloaded}\n",
    "deploy/ansible/roles/prometheus/files/faas.rules.yml": "groups: []\n",
    "deploy/ansible/roles/control_plane_service/tasks/main.yml":
        "- ansible.builtin.template:\n    src: ../../_shared/templates/99-node-name.conf.j2\n"
        "    dest: /etc/systemd/system/faas-apid.service.d/99-node-name.conf\n"
        "- ansible.builtin.copy:\n    src: \"{{ playbook_dir }}/../statuspage/index.html\"\n"
        "    dest: /etc/faas/statuspage/index.html\n"
        "  notify: restart faas-apid\n",
    "deploy/ansible/roles/control_plane_service/handlers/main.yml":
        "- name: daemon-reload\n  listen:\n    - restart faas-apid\n  ansible.builtin.systemd_service: {daemon_reload: true}\n",
    "deploy/ansible/roles/control_plane_service/files/faas-apid.service": "[Service]\n",
    "deploy/ansible/roles/s3_gateway_service/tasks/main.yml":
        "- ansible.builtin.copy: {src: s3.env, dest: /etc/faas/s3.env}\n"
        "  notify:\n    - restart faas-gatewayd-public\n",
    "deploy/ansible/roles/s3_gateway_service/files/s3.env": "X=1\n",
    "deploy/ansible/roles/gatewayd_public_service/tasks/main.yml":
        "- ansible.builtin.template:\n    src: ../../_shared/templates/99-node-name.conf.j2\n"
        "    dest: /etc/systemd/system/faas-gatewayd-public.service.d/99-node-name.conf\n",
    "deploy/ansible/roles/gatewayd_public_service/handlers/main.yml":
        "- name: try-restart gatewayd-public\n  listen: restart faas-gatewayd-public\n"
        "  ansible.builtin.command: systemctl try-restart faas-gatewayd-public.service\n",
    "deploy/ansible/roles/vmmd_service/tasks/main.yml":
        "- ansible.builtin.include_role:\n    name: vmmd_jail\n",
    "deploy/ansible/roles/vmmd_jail/tasks/main.yml": "- ansible.builtin.debug: {msg: jail}\n",
    "deploy/systemd/faas-pg-basebackup.service": "[Service]\n",
    "deploy/systemd/faas-gatewayd-internal.service": "[Service]\n",
    "deploy/statuspage/index.html": "<html></html>\n",
    "docs/quickstart.md": "docs\n",
}


def tree(**changes):
    files = dict(FILES)
    for path, body in changes.items():
        path = path.replace("__", "/")
        if body is None:
            files.pop(path, None)
        else:
            files[path] = body
    return hr.dict_tree(files)


def edit(path, body):
    return {path.replace("/", "__"): body}


def selected(group, **changes):
    base = tree()
    head = tree(**changes)
    new = hr.group_hashes(head, group)
    return hr.changed(hr.group_hashes(base, group), new, hr.notify_targets(head, new["order"]))


class ChangedTest(unittest.TestCase):
    CASES = [
        # name, group, change, all, roles
        ("unrelated doc selects nothing", "control_plane", edit("docs/quickstart.md", "new\n"), False, []),
        ("role README is not content", "control_plane",
         edit("deploy/ansible/roles/postgres/README.md", "new\n"), False, []),
        ("unread unit tree file selects nothing", "control_plane",
         edit("deploy/systemd/faas-gatewayd-internal.service", "[Service]\nX=1\n"), False, []),
        ("role task change selects only that role", "control_plane",
         edit("deploy/ansible/roles/postgres/tasks/main.yml", FILES["deploy/ansible/roles/postgres/tasks/main.yml"] + "- debug: {}\n"),
         False, ["postgres"]),
        ("defaults change is detected", "control_plane",
         edit("deploy/ansible/roles/postgres/defaults/main.yml", "postgres_max_connections: 180\n"), False, ["postgres"]),
        ("template change is detected", "control_plane",
         edit("deploy/ansible/roles/nftables/templates/rules.nft.j2", "table inet faas { }\n"), False, ["nftables"]),
        ("role file change is detected", "control_plane",
         edit("deploy/ansible/roles/prometheus/files/faas.rules.yml", "groups: [x]\n"), False, ["prometheus"]),
        ("file named through role_path outside the role", "control_plane",
         edit("deploy/systemd/faas-pg-basebackup.service", "[Service]\nNice=5\n"), False, ["postgres"]),
        ("file named through playbook_dir outside the role", "control_plane",
         edit("deploy/statuspage/index.html", "<html>new</html>\n"), False, ["control_plane_service"]),
        ("shared template selects every role that renders it", "control_plane",
         edit("deploy/ansible/roles/_shared/templates/99-node-name.conf.j2", "[Service]\nX=1\n"),
         False, ["control_plane_service", "gatewayd_public_service"]),
        ("shared task library selects its includers", "control_plane",
         edit("deploy/ansible/tasks/validate_udp_policy.yml", "- debug: {}\n"), False, ["nftables"]),
        ("a role that notifies another role's handler selects it", "control_plane",
         edit("deploy/ansible/roles/s3_gateway_service/files/s3.env", "X=2\n"), False,
         ["s3_gateway_service", "gatewayd_public_service"]),
        ("comment in a role entry selects nothing", "control_plane",
         edit("deploy/ansible/bootstrap.yml", BOOTSTRAP.replace("# Shared preconditions.", "# Preconditions.")),
         False, []),
        ("a role entry's when: selects that role", "control_plane",
         edit("deploy/ansible/bootstrap.yml", BOOTSTRAP.replace("default(true)", "default(false)")),
         False, ["postgres"]),
        ("a new role entry selects the new role", "control_plane",
         {**edit("deploy/ansible/bootstrap.yml", BOOTSTRAP.replace("    - role: prometheus\n",
                                                                   "    - role: prometheus\n    - role: grafana\n")),
          **edit("deploy/ansible/roles/grafana/tasks/main.yml", "- debug: {}\n")},
         False, ["grafana"]),
        ("compute play change leaves the control plane alone", "control_plane",
         edit("deploy/ansible/bootstrap.yml", BOOTSTRAP.replace("    - role: vmmd_service\n", "")), False, []),
        ("compute role change selects nothing on the control plane", "control_plane",
         edit("deploy/ansible/roles/vmmd_service/tasks/main.yml", "- debug: {}\n"), False, []),
        ("play body change is a globals change", "control_plane",
         edit("deploy/ansible/bootstrap.yml", BOOTSTRAP.replace("name: gather", "name: gather facts")), True, None),
        ("ansible.cfg is a globals change", "control_plane",
         edit("deploy/ansible/ansible.cfg", "[defaults]\nforks = 2\n"), True, None),
        ("collection pins are a globals change", "compute_nodes",
         edit("deploy/ansible/requirements.yml", "collections: [x]\n"), True, None),
        ("group_vars are the group's globals", "control_plane",
         edit("deploy/ansible/group_vars/control_plane/backup.yml", "faas_backup: false\n"), True, None),
        ("another group's vars are not", "control_plane",
         edit("deploy/ansible/group_vars/compute_nodes/log.yml", "faas_log: false\n"), False, []),
        ("the converge playbook is a globals change", "control_plane",
         edit("deploy/ansible/control_plane_converge.yml", "- import_playbook: bootstrap.yml\n# x\n- hosts: all\n"),
         True, None),
        ("an included role's change selects its includer", "compute_nodes",
         edit("deploy/ansible/roles/vmmd_jail/tasks/main.yml", "- debug: {msg: jail2}\n"), False, ["vmmd_service"]),
        ("a role shared by both plays changes in both", "compute_nodes",
         edit("deploy/ansible/roles/nftables/templates/rules.nft.j2", "table inet faas { }\n"), False, ["nftables"]),
    ]

    def test_cases(self):
        for name, group, change, want_all, roles in self.CASES:
            with self.subTest(name):
                got = selected(group, **change)
                self.assertEqual(got["all"], want_all)
                if want_all:
                    self.assertEqual(got["roles"], hr.group_hashes(tree(**change), group)["order"])
                else:
                    self.assertEqual(got["roles"], roles)

    def test_hashes_depend_on_content_only(self):
        a = hr.group_hashes(tree(), "control_plane")
        b = hr.group_hashes(hr.dict_tree(dict(reversed(list(FILES.items())))), "control_plane")
        self.assertEqual(a, b)
        self.assertEqual(a["order"], ["nftables", "postgres", "prometheus", "control_plane_service",
                                      "s3_gateway_service", "gatewayd_public_service"])
        for value in [a["globals"], *a["roles"].values()]:
            self.assertRegex(value, r"^sha256:[0-9a-f]{64}$")

    def test_unknown_or_other_format_record_converges_everything(self):
        new = hr.group_hashes(tree(), "control_plane")
        for record in (None, {}, dict(new, format_version=0), dict(new, group="compute_nodes")):
            with self.subTest(record=record):
                self.assertEqual(hr.changed(record, new), {"all": True, "roles": new["order"]})
        self.assertEqual(hr.changed(new, new), {"all": False, "roles": []})

    def test_owners_map_paths_to_roles_and_globals(self):
        owners = hr.group_owners(tree(), "control_plane")
        self.assertEqual(owners["deploy/systemd/faas-pg-basebackup.service"], ["postgres"])
        self.assertEqual(owners["deploy/ansible/roles/_shared/templates/99-node-name.conf.j2"],
                         ["control_plane_service", "gatewayd_public_service"])
        self.assertEqual(owners["deploy/ansible/bootstrap.yml"], ["*"])
        self.assertEqual(owners["deploy/ansible/group_vars/control_plane/backup.yml"], ["*"])
        for path in ("deploy/systemd/faas-gatewayd-internal.service", "deploy/ansible/roles/postgres/README.md",
                     "deploy/ansible/roles/vmmd_service/tasks/main.yml", "docs/quickstart.md"):
            self.assertNotIn(path, owners)
        compute = hr.group_owners(tree(), "compute_nodes")
        self.assertEqual(compute["deploy/ansible/roles/vmmd_jail/tasks/main.yml"], ["vmmd_service"])

    def test_unresolved_references_are_reported(self):
        t = tree(**edit("deploy/ansible/roles/postgres/tasks/main.yml",
                        "- ansible.builtin.copy: {src: ../../missing/file.conf, dest: /etc/x}\n"))
        roles = hr.Roles(t)
        hr.group_owners(t, "control_plane", roles)
        self.assertEqual(roles.unresolved, {"postgres": ["deploy/ansible/roles/postgres/tasks/main.yml: ../../missing/file.conf"]})

    def test_comment_references_are_ignored(self):
        roles = hr.Roles(tree())
        self.assertNotIn("postgres", roles.unresolved)
        self.assertEqual(sorted(p for p in roles.inputs("postgres") if not p.startswith("deploy/ansible/roles/")),
                         ["deploy/systemd/faas-pg-basebackup.service"])

    def test_git_tree_matches_checkout(self):
        with tempfile.TemporaryDirectory() as root:
            for path, body in FILES.items():
                target = pathlib.Path(root, path)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(body, encoding="utf-8")
            for args in (["init", "-q"], ["add", "-A"],
                         ["-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x"]):
                subprocess.run(["git", *args], cwd=root, check=True, capture_output=True)
            for group in hr.GROUPS:
                want = hr.group_hashes(hr.dict_tree(FILES), group)
                self.assertEqual(hr.group_hashes(hr.git_tree("HEAD", root), group), want)
                self.assertEqual(hr.group_hashes(hr.fs_tree(root), group), want)


class RepositoryTest(unittest.TestCase):
    """The real deploy tree: every reference a host role makes resolves, so
    no input escapes its role's hash."""

    @classmethod
    def setUpClass(cls):
        cls.tree = hr.fs_tree(str(REPO))
        cls.roles = hr.Roles(cls.tree)
        cls.owners = {g: hr.group_owners(cls.tree, g, cls.roles) for g in hr.GROUPS}

    def test_every_role_reference_resolves(self):
        self.assertEqual(self.roles.unresolved, {})

    def test_plays_name_existing_roles(self):
        for group in hr.GROUPS:
            _, order, _ = hr.group_plays(self.tree, group)
            for role in order:
                with self.subTest(group=group, role=role):
                    self.assertTrue(self.tree.is_dir(f"{hr.ROLES}/{role}"), f"{role} is not a role")
        _, cp, _ = hr.group_plays(self.tree, "control_plane")
        _, compute, _ = hr.group_plays(self.tree, "compute_nodes")
        self.assertIn("control_plane_service", cp)
        self.assertNotIn("vmmd_service", cp)
        self.assertIn("vmmd_service", compute)

    def test_known_external_inputs_are_owned(self):
        cp = self.owners["control_plane"]
        self.assertIn("postgres_backup", cp["deploy/systemd/faas-pg-basebackup.service"])
        self.assertIn("control_plane_service", cp["deploy/statuspage/index.html"])
        self.assertIn("control_plane_service", cp["deploy/ansible/roles/_shared/files/storage.env.example"])
        self.assertNotIn("deploy/systemd/faas-gatewayd-internal.service", cp)
        compute = self.owners["compute_nodes"]
        self.assertIn("compute_only_service", compute["deploy/ansible/roles/control_plane_service/files/schedd.toml.example"])


if __name__ == "__main__":
    unittest.main()
