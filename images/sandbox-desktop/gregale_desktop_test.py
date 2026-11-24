"""Unit tests for gregale-desktop's argument checks (ADR-20261124075005 §4).

They need no X server: they check the xdotool argv each input command
builds and the input each command refuses. The image smoke test runs the
real desktop.
"""

import importlib.machinery
import importlib.util
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
_loader = importlib.machinery.SourceFileLoader("gregale_desktop", os.path.join(HERE, "gregale-desktop"))
_spec = importlib.util.spec_from_loader("gregale_desktop", _loader)
gd = importlib.util.module_from_spec(_spec)
_loader.exec_module(gd)

SIZE = (1024, 768)


def argv_for(*cli):
    args = gd.parser().parse_args(list(cli))
    return gd.xdotool_argv(args.cmd, args, SIZE)


class InputArgv(unittest.TestCase):
    def test_commands(self):
        cases = {
            ("click", "10", "20"): ["xdotool", "mousemove", "--sync", "10", "20", "click", "1"],
            ("click", "10", "20", "--button", "right", "--double"):
                ["xdotool", "mousemove", "--sync", "10", "20", "click", "--repeat", "2", "--delay", "80", "3"],
            ("move", "0", "767"): ["xdotool", "mousemove", "--sync", "0", "767"],
            ("down", "--button", "middle"): ["xdotool", "mousedown", "2"],
            ("up",): ["xdotool", "mouseup", "1"],
            ("drag", "1", "2", "3", "4"): ["xdotool", "mousemove", "--sync", "1", "2", "mousedown", "1", "sleep", "0.1",
                                           "mousemove", "--sync", "3", "4", "sleep", "0.1", "mouseup", "1"],
            ("scroll", "down"): ["xdotool", "click", "--repeat", "1", "--delay", "30", "5"],
            ("scroll", "left", "3"): ["xdotool", "click", "--repeat", "3", "--delay", "30", "6"],
            ("type", "--", "-rf /; $(reboot)"): ["xdotool", "type", "--clearmodifiers", "--delay", "8", "--", "-rf /; $(reboot)"],
            ("key", "enter", "ctrl+c", "ctrl+shift+T", "+"):
                ["xdotool", "key", "--clearmodifiers", "--", "Return", "ctrl+c", "ctrl+shift+T", "plus"],
        }
        for cli, want in cases.items():
            with self.subTest(cli=cli):
                self.assertEqual(argv_for(*cli), want)

    def test_refused(self):
        refused = [
            ("click", "1024", "0"),
            ("click", "-1", "0"),
            ("click", "1.5", "2"),
            ("move", "0", "768"),
            ("drag", "0", "0", "2000", "0"),
            ("scroll", "down", "0"),
            ("scroll", "down", "51"),
            ("type", "--", "x" * (gd.TYPE_MAX_CHARS + 1)),
            ("key",),
            ("key", "ctrl+"),
            ("key", "$(reboot)"),
            ("key", "a b"),
        ]
        for cli in refused:
            with self.subTest(cli=cli):
                with self.assertRaises(gd.UsageError):
                    argv_for(*cli)

    def test_parser_refuses(self):
        for cli in (["click", "1"], ["scroll", "sideways"], ["click", "1", "2", "--button", "back"], ["nope"], []):
            with self.subTest(cli=cli):
                with self.assertRaises(gd.UsageError):
                    gd.parser().parse_args(cli)


class Resolution(unittest.TestCase):
    def test_bounds(self):
        self.assertEqual(gd.parse_resolution("1024x768"), (1024, 768))
        self.assertEqual(gd.parse_resolution("1920x1080"), (1920, 1080))
        self.assertEqual(gd.parse_resolution("640x480"), (640, 480))
        for bad in ("639x480", "1921x1080", "1024x1081", "1024*768", "01024x768", "1024x768 ", "", None):
            with self.subTest(bad=bad):
                with self.assertRaises(gd.UsageError):
                    gd.parse_resolution(bad)


class Launch(unittest.TestCase):
    def test_refuses_paths_and_options(self):
        for app in ("/bin/sh", "../x", "-x", "a b", "x;y"):
            with self.subTest(app=app):
                with self.assertRaises(gd.UsageError):
                    gd.cmd_launch(gd.parse(["launch", app]))

    def test_open_refuses_options(self):
        with self.assertRaises(gd.UsageError):
            gd.cmd_open(gd.parse(["open", "--", "--help"]))

    def test_launch_args_after_dashes(self):
        args = gd.parse(["launch", "chromium", "--", "--incognito", "https://example.com"])
        self.assertEqual(args.args, ["--incognito", "https://example.com"])


class Processes(unittest.TestCase):
    def test_vnc_is_loopback_and_view_only_takes_no_input(self):
        procs = dict(gd.desktop_processes())
        self.assertIn("-localhost", procs["x11vnc"])
        self.assertNotIn("-viewonly", procs["x11vnc"])
        self.assertIn("-viewonly", procs["x11vnc-viewonly"])
        self.assertEqual(procs["novnc"][-2:], ["127.0.0.1:6080", "127.0.0.1:5900"])
        self.assertEqual(procs["novnc-viewonly"][-2:], ["127.0.0.1:6081", "127.0.0.1:5901"])


if __name__ == "__main__":
    unittest.main()
