"""Black-box tests: real jj invokes the real jj-patch-interactive, never a fake editor.

Run: python3 -m unittest discover -s integration -v
JJ_PATCH_INTERACTIVE_BIN and JJ_BIN may override the executables. No third-party modules.
"""
from __future__ import annotations

import errno
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import signal
import stat
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
PATCH = Path(os.environ.get("JJ_PATCH_INTERACTIVE_BIN", str(ROOT / "jj-patch-interactive"))).resolve()
JJ = os.environ.get("JJ_BIN") or shutil.which("jj") or "jj"
GIT = shutil.which("git") or "git"
TIMEOUT = int(os.environ.get("JJ_PATCH_INTERACTIVE_TEST_TIMEOUT", "30"))
REGULAR = "100644"
BASE = {"a.txt": (REGULAR, b"alpha old\n"), "b.txt": (REGULAR, b"beta old\n"),
        "untouched.txt": (REGULAR, b"must survive\n")}
FULL = {**BASE, "a.txt": (REGULAR, b"alpha new\n"), "b.txt": (REGULAR, b"beta new\n")}
FIRST = {**BASE, "a.txt": FULL["a.txt"]}
SECOND = {**BASE, "b.txt": FULL["b.txt"]}


def setUpModule():
    # A missing build is an error, not a misleading all-green skipped suite.
    if not PATCH.is_file() or not os.access(PATCH, os.X_OK):
        raise RuntimeError(f"Build jj-patch-interactive first or set JJ_PATCH_INTERACTIVE_BIN (missing executable: {PATCH})")


class RepositoryCase(unittest.TestCase):
    three_way = False
    instructions = True
    colocated = False

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="jj-patch-interactive-integration-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.repo = self.root / "repo"
        self.home = self.root / "home"
        self.home.mkdir()
        self.config = self.root / "jj.toml"
        self.env = {k: v for k, v in os.environ.items()
                    if not k.startswith(("JJ_", "GIT_", "XDG_"))
                    and k not in ("HOME", "EDITOR", "VISUAL", "PAGER")}
        self.env.update(HOME=str(self.home), XDG_CONFIG_HOME=str(self.home / "config"),
                        XDG_CACHE_HOME=str(self.home / "cache"),
                        JJ_CONFIG=str(self.config), GIT_CONFIG_NOSYSTEM="1",
                        GIT_CONFIG_GLOBAL=os.devnull, LC_ALL="C", TERM="xterm-256color",
                        NO_COLOR="1", PAGER="cat", EDITOR="false", VISUAL="false")
        self.configure()
        self.jj("git", "init", "--colocate" if self.colocated else "--no-colocate",
                str(self.repo), cwd=self.root)
        self.gitdir = self.repo / (".git" if self.colocated else ".jj/repo/store/git")
        self.write_tree(BASE)
        self.jj("describe", "-m", "base")
        self.base_id = self.commit_id()
        self.jj("new", "-m", "work")
        self.write_tree(FULL)
        self.snapshot()
        self.start_id = self.commit_id()

    def configure(self, context=None, *, three_way=None, instructions=None):
        if three_way is not None:
            self.three_way = three_way
        if instructions is not None:
            self.instructions = instructions
        args = (["--context", context] if context else [])
        if self.three_way:
            args += ["--output", "$output"]
        args += ["$left", "$right"]
        # Defaults exercise jj's real JJ-INSTRUCTIONS; false uses the actual
        # jj setting, ui.diff-instructions (not a merge-tools setting).
        self.config.write_text(
            '[user]\nname = "Integration Test"\nemail = "test@example.invalid"\n'
            '[ui]\ncolor = "never"\npaginate = "never"\neditor = "false"\n'
            'diff-editor = "jj-patch-interactive"\n' +
            ('' if self.instructions else 'diff-instructions = false\n') +
            '[snapshot]\nmax-new-file-size = "10MiB"\n'
            '[merge-tools.jj-patch-interactive]\nprogram = ' + json.dumps(str(PATCH)) + '\n'
            'edit-invocation-mode = "dir"\nedit-args = ' + json.dumps(args) + '\n')

    def run_process(self, args, *, input=b"", cwd=None, check=True):
        result = subprocess.run([str(x) for x in args], input=input,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                cwd=cwd or self.repo, env=self.env, timeout=TIMEOUT)
        if check and result.returncode:
            self.fail(f"{args!r} exited {result.returncode}\n"
                      f"stdout: {result.stdout.decode(errors='replace')}\n"
                      f"stderr: {result.stderr.decode(errors='replace')}")
        return result

    def jj(self, *args, input=b"", cwd=None, check=True):
        return self.run_process([JJ, "--no-pager", *args], input=input, cwd=cwd, check=check)

    def git(self, *args):
        return self.run_process([GIT, "--git-dir=" + str(self.gitdir), *args]).stdout

    def snapshot(self):
        self.jj("status")

    def commit_id(self, revision="@"):
        return self.jj("log", "--no-graph", "-r", revision,
                       "-T", "commit_id").stdout.decode().strip()

    def parents(self, revision="@"):
        return self.jj("log", "--no-graph", "-r", f"parents({revision})",
                       "-T", 'commit_id ++ "\\n"').stdout.decode().splitlines()

    def tree(self, revision="@"):
        """Read Git objects without checkout/index changes; compare modes + bytes.

        Conflicts have jj-specific backing objects: conflict tests instead use
        jj file show and the conflict template keyword.
        """
        commit = self.commit_id(revision)
        result = {}
        for entry in self.git("ls-tree", "-rz", commit).split(b"\0"):
            if entry:
                meta, name = entry.split(b"\t", 1)
                mode, kind, oid = meta.split()
                self.assertEqual(kind, b"blob")
                result[os.fsdecode(name)] = (mode.decode(), self.git("cat-file", "blob", oid.decode()))
        return result

    def disk_tree(self):
        result = {}
        for directory, dirs, files in os.walk(self.repo, followlinks=False):
            dirs[:] = [name for name in dirs if name not in (".jj", ".git")]
            # Include symlinks-to-directories without following them.
            links = [name for name in dirs if (Path(directory) / name).is_symlink()]
            dirs[:] = [name for name in dirs if name not in links]
            for name in files + links:
                path = Path(directory) / name
                rel = str(path.relative_to(self.repo))
                if path.is_symlink():
                    result[rel] = ("120000", os.fsencode(os.readlink(path)))
                else:
                    mode = "100755" if path.stat().st_mode & stat.S_IXUSR else REGULAR
                    result[rel] = (mode, path.read_bytes())
        return result

    def write_tree(self, tree):
        for name in self.disk_tree():
            if name not in tree:
                (self.repo / name).unlink()
        for name, (mode, data) in tree.items():
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            if path.is_symlink() or path.exists():
                path.unlink()
            if mode == "120000":
                path.symlink_to(os.fsdecode(data))
            else:
                path.write_bytes(data)
                path.chmod(0o755 if mode == "100755" else 0o644)

    def assert_tree(self, expected, revision="@", *, disk=False):
        self.assertEqual(self.tree(revision), expected)
        if disk:
            self.assertEqual(self.disk_tree(), expected)
        if "JJ-INSTRUCTIONS" not in expected:
            self.assertNotIn("JJ-INSTRUCTIONS", self.tree(revision))

    def safety_state(self):
        """State that a successful or failed editor must not independently alter."""
        return {
            "base": self.tree(self.base_id),
            "index": (self.gitdir / "index").read_bytes() if (self.gitdir / "index").exists() else None,
            "gitconfig": (self.gitdir / "config").read_bytes(),
            "jjconfig": self.config.read_bytes(),
        }

    def edit(self, *args, input=b"y\nn\n", check=True):
        before = self.safety_state()
        result = self.jj(*args, input=input, check=check)
        self.assertEqual(self.safety_state(), before, "editor touched caller config/index/base")
        self.assertEqual((self.repo / ".git").exists(), self.colocated,
                         "editor changed caller Git repository layout")
        return result

    def assert_cancelled(self, args, input):
        self.snapshot()
        before = self.commit_id()
        before_tree = self.tree()
        before_disk = self.disk_tree()
        before_graph = self.jj("log", "--no-graph", "-r", "all()", "-T",
                               'commit_id ++ "\\n"').stdout
        result = self.edit(*args, input=input, check=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.commit_id(), before)
        self.assertEqual(self.tree(), before_tree)
        self.assertEqual(self.disk_tree(), before_disk)
        self.assertEqual(self.jj("log", "--no-graph", "-r", "all()", "-T",
                                 'commit_id ++ "\\n"').stdout, before_graph)


class WorkflowTests:
    """The same workflow contract runs in both two- and three-directory modes."""

    def test_diffedit_partial(self):
        self.edit("diffedit")
        self.assert_tree(FIRST, disk=True)
        self.assertEqual(self.parents(), [self.base_id])

    def test_diffedit_reject_all(self):
        self.edit("diffedit", input=b"n\nn\n")
        self.assert_tree(BASE, disk=True)

    def test_diffedit_accept_all_is_noop(self):
        self.edit("diffedit", input=b"A\n")
        self.assert_tree(FULL, disk=True)
        self.assertEqual(self.commit_id(), self.start_id)

    def test_q_without_selection_is_reject_all(self):
        self.edit("diffedit", input=b"q\n")
        self.assert_tree(BASE, disk=True)

    def test_q_saves_prior_selections(self):
        self.edit("diffedit", input=b"y\nq\n")
        self.assert_tree(FIRST, disk=True)

    def test_diffedit_fileset_does_not_revert_excluded_paths(self):
        self.edit("diffedit", 'glob:"a.*"', input=b"n\n")
        self.assert_tree(SECOND, disk=True)

    def test_diffedit_revision_preserves_descendant_content(self):
        self.jj("new", "-m", "descendant")
        descendant = {**FULL, "child.txt": (REGULAR, b"descendant\n")}
        self.write_tree(descendant)
        self.snapshot()
        self.edit("diffedit", "-r", "@-", "--restore-descendants")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(descendant, disk=True)

    def test_diffedit_from_to(self):
        self.edit("diffedit", "--from", "@-", "--to", "@")
        self.assert_tree(FIRST, disk=True)

    def test_split_default_is_interactive(self):
        self.edit("split", "-m", "selected")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)
        self.assertEqual(self.parents("@-"), [self.base_id])

    def test_split_parallel(self):
        self.edit("split", "--parallel", "-m", "selected")
        self.assert_tree(FIRST, 'description(substring:"selected")')
        self.assert_tree(SECOND, disk=True)
        self.assertEqual(self.parents(), [self.base_id])
        self.assertEqual(self.parents('description(substring:"selected")'), [self.base_id])

    def test_split_revision_fileset(self):
        self.jj("new", "-m", "descendant")
        self.edit("split", "-r", "@-", "-i", "-m", "selected", "a.txt", input=b"A\n")
        self.assert_tree(FIRST, 'description(substring:"selected")')
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_split_reject_all(self):
        self.edit("split", "-m", "selected", input=b"n\nn\n")
        self.assert_tree(BASE, "@-")
        self.assert_tree(FULL, disk=True)

    def test_split_accept_all(self):
        self.edit("split", "-m", "selected", input=b"A\n")
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_squash_partial(self):
        self.edit("squash", "-i", "-m", "squashed", "--keep-emptied")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_squash_reject_all_is_jj_error(self):
        self.assert_cancelled(["squash", "-i", "-m", "squashed"], b"n\nn\n")
        self.assert_tree(BASE, "@-")
        self.assert_tree(FULL, disk=True)
        self.assertEqual(self.commit_id(), self.start_id)

    def test_squash_accept_all_keep_emptied(self):
        self.edit("squash", "-i", "-m", "squashed", "--keep-emptied", input=b"A\n")
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_squash_revision_fileset(self):
        self.jj("new", "-m", "descendant")
        self.edit("squash", "-i", "-r", "@-", "-m", "squashed", "a.txt", input=b"A\n")
        self.assert_tree(FIRST, "@--")
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_squash_from_into(self):
        self.edit("squash", "-i", "--from", "@", "--into", "@-", "-m", "squashed")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_restore_partial_is_reverse_direction(self):
        self.edit("restore", "-i")
        self.assert_tree(SECOND, disk=True)

    def test_restore_reject_all_is_noop(self):
        self.edit("restore", "-i", input=b"n\nn\n")
        self.assert_tree(FULL, disk=True)
        self.assertEqual(self.commit_id(), self.start_id)

    def test_restore_accept_all(self):
        self.edit("restore", "-i", input=b"A\n")
        self.assert_tree(BASE, disk=True)

    def test_restore_explicit_from_into_fileset(self):
        self.edit("restore", "-i", "--from", "@-", "--into", "@", "a.txt", input=b"A\n")
        self.assert_tree(SECOND, disk=True)

    def test_restore_changes_in_preserves_descendant(self):
        self.jj("new", "-m", "descendant")
        self.edit("restore", "-i", "--changes-in", "@-", "--restore-descendants")
        self.assert_tree(SECOND, "@-")
        self.assert_tree(FULL, disk=True)

    def test_commit_partial(self):
        self.edit("commit", "-i", "-m", "selected")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_commit_reject_all(self):
        self.edit("commit", "-i", "-m", "selected", input=b"n\nn\n")
        self.assert_tree(BASE, "@-")
        self.assert_tree(FULL, disk=True)

    def test_commit_accept_all(self):
        self.edit("commit", "-i", "-m", "selected", input=b"A\n")
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_commit_fileset(self):
        self.edit("commit", "-i", "-m", "selected", "a.txt", input=b"A\n")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_absorb_partial(self):
        self.edit("absorb", "-i")
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_absorb_reject_all_is_jj_error(self):
        self.assert_cancelled(["absorb", "-i"], b"n\nn\n")

    def test_absorb_accept_all(self):
        self.edit("absorb", "-i", input=b"A\n")
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_absorb_from_into_fileset(self):
        self.jj("new", "-m", "descendant")
        self.edit("absorb", "-i", "--from", "@-", "--into", "@--", "a.txt", input=b"A\n")
        self.assert_tree(FIRST, "@--")
        self.assert_tree(FULL, "@-")
        self.assert_tree(FULL, disk=True)

    def test_all_workflows_abort_and_eof_leave_revisions_unchanged(self):
        commands = [
            ["diffedit"], ["split", "-m", "selected"],
            ["split", "--parallel", "-m", "selected"],
            ["squash", "-i", "-m", "squashed"], ["restore", "-i"],
            ["commit", "-i", "-m", "selected"], ["absorb", "-i"],
        ]
        for command in commands:
            for answer in (b"Q\n", b"", b"y\nQ\n", b"y\n"):
                with self.subTest(command=command, answer=answer):
                    self.assert_cancelled(command, answer)

    def test_tool_option_implies_interactive(self):
        commands = [
            ["diffedit"], ["split", "-m", "selected", "a.txt"],
            ["squash", "-m", "squashed"], ["restore"],
            ["commit", "-m", "selected"], ["absorb"],
        ]
        for command in commands:
            with self.subTest(command=command):
                self.assert_cancelled([*command, "--tool", "jj-patch-interactive"], b"Q\n")

    def test_added_only_nested_empty_binary_symlink_executable_accept(self):
        added = {"nested/deep/text.txt": (REGULAR, b"added text\n"),
                 "empty": (REGULAR, b""), "binary": (REGULAR, b"\x00\xff\x01hello\x00"),
                 "run.sh": ("100755", b"#!/bin/sh\necho hi\n"),
                 "link": ("120000", b"nested/deep/text.txt"),
                 "dangling": ("120000", b"does-not-exist"),
                 "sp ace\tunicode-λ.txt": (REGULAR, b"no final newline")}
        self.write_tree({**BASE, **added})
        self.snapshot()
        self.edit("diffedit", input=b"A\n")
        self.assert_tree({**BASE, **added}, disk=True)

    def test_added_only_all_rejected_leaves_no_files(self):
        added = {"nested/deep/new": (REGULAR, b"new\n"), "empty": (REGULAR, b""),
                 "binary": (REGULAR, b"\0binary"), "link": ("120000", b"missing"),
                 "run.sh": ("100755", b"#!/bin/sh\n")}
        self.write_tree({**BASE, **added})
        self.snapshot()
        self.edit("diffedit", input=b"q\n")
        self.assert_tree(BASE, disk=True)

    def test_deletions_accept_all(self):
        self.write_tree({"untouched.txt": BASE["untouched.txt"]})
        self.snapshot()
        self.edit("diffedit", input=b"A\n")
        self.assert_tree({"untouched.txt": BASE["untouched.txt"]}, disk=True)

    def test_deletions_reject_all_restores_paths(self):
        self.write_tree({"untouched.txt": BASE["untouched.txt"]})
        self.snapshot()
        self.edit("diffedit", input=b"q\n")
        self.assert_tree(BASE, disk=True)

    def test_mode_only_change_rejected(self):
        self.write_tree({**BASE, "a.txt": ("100755", BASE["a.txt"][1])})
        self.snapshot()
        self.edit("diffedit", input=b"n\n")
        self.assert_tree(BASE, disk=True)

    def test_mode_only_change_accepted(self):
        expected = {**BASE, "a.txt": ("100755", BASE["a.txt"][1])}
        self.write_tree(expected)
        self.snapshot()
        self.edit("diffedit", input=b"A\n")
        self.assert_tree(expected, disk=True)

    def test_symlink_replacement_and_binary_modification(self):
        self.write_tree({**BASE, "a.txt": ("120000", b"b.txt"),
                         "b.txt": (REGULAR, b"\0\xffbinary\0")})
        self.snapshot()
        self.edit("diffedit", input=b"y\nn\n")
        self.assert_tree({**BASE, "a.txt": ("120000", b"b.txt")}, disk=True)

    def test_real_tracked_instructions_name_is_not_ignored(self):
        expected = {**BASE, "JJ-INSTRUCTIONS": (REGULAR, b"This is user data, not editor instructions.\n")}
        self.write_tree(expected)
        self.snapshot()
        self.edit("diffedit", input=b"A\n")
        self.assert_tree(expected, disk=True)
        self.edit("diffedit", input=b"q\n")
        self.assert_tree(BASE, disk=True)

    def test_deleted_instructions_path_collision_and_recovery(self):
        parent = {**BASE, "JJ-INSTRUCTIONS": (REGULAR, b"real versioned instructions\n")}
        self.write_tree(parent)
        self.jj("describe", "-m", "versioned instruction file")
        self.jj("new", "-m", "delete instruction file")
        (self.repo / "JJ-INSTRUCTIONS").unlink()
        self.snapshot()
        before = self.commit_id()
        if self.instructions:
            # jj has overwritten the deleted path with generated help and will
            # remove it after tool exit. Silently restoring the left file here
            # would lose the user's choice: fail before jj snapshots anything.
            result = self.edit("diffedit", input=b"q\n", check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b"ui.diff-instructions=false", result.stderr)
            self.assertEqual(self.commit_id(), before)
            self.assert_tree(BASE, disk=True)
            self.configure(instructions=False)
        self.edit("diffedit", input=b"q\n")
        self.assert_tree(parent, disk=True)
        (self.repo / "JJ-INSTRUCTIONS").unlink()
        self.snapshot()
        self.edit("diffedit", input=b"A\n")
        self.assert_tree(BASE, disk=True)

    def test_preexisting_private_git_index_is_untouched(self):
        self.git("read-tree", self.base_id)
        index = (self.gitdir / "index").read_bytes()
        self.edit("diffedit")
        self.assert_tree(FIRST, disk=True)
        self.assertEqual((self.gitdir / "index").read_bytes(), index)

    def test_global_regex_filter_accepts_only_matches(self):
        self.edit("diffedit", input=b"G beta\nA\n")
        self.assert_tree(SECOND, disk=True)

    def test_global_regex_filter_no_matches_selects_nothing(self):
        self.edit("diffedit", input=b"G NO_SUCH_PATTERN_8271\nq\n")
        self.assert_tree(BASE, disk=True)

    def test_same_file_q_preserves_only_prior_hunk(self):
        old = b"first old\n" + b"unchanged\n" * 12 + b"last old\n"
        new = old.replace(b"first old", b"first new").replace(b"last old", b"last new")
        self._hunk_fixture(old, new)
        self.edit("diffedit", input=b"y\nq\n")
        self.assert_tree({"hunks.txt": (REGULAR, old.replace(b"first old", b"first new"))}, disk=True)

    def _hunk_fixture(self, old, new):
        self.write_tree({"hunks.txt": (REGULAR, old)})
        self.jj("describe", "-m", "hunk base")
        self.jj("new", "-m", "hunks")
        self.write_tree({"hunks.txt": (REGULAR, new)})
        self.snapshot()

    def test_split_hunk_s(self):
        old = b"one old\nanchor 1\nanchor 2\ntwo old\n"
        new = old.replace(b"one old", b"one new").replace(b"two old", b"two new")
        self._hunk_fixture(old, new)
        self.edit("diffedit", input=b"s\ny\nn\n")
        self.assert_tree({"hunks.txt": (REGULAR, old.replace(b"one old", b"one new"))}, disk=True)

    def test_autosplit_then_filter(self):
        old = b"one old\nanchor 1\nanchor 2\ntwo old\n"
        new = old.replace(b"one old", b"one new").replace(b"two old", b"two new")
        self._hunk_fixture(old, new)
        self.edit("diffedit", input=b"S\nG two\nA\n")
        self.assert_tree({"hunks.txt": (REGULAR, old.replace(b"two old", b"two new"))}, disk=True)

    def test_conflict_materialization_accept_all_and_abort_preserve_conflict(self):
        self.jj("new", self.base_id, "-m", "side one")
        self.write_tree({**BASE, "a.txt": (REGULAR, b"side one\n")})
        self.snapshot()
        side_one = self.commit_id()
        self.jj("new", self.base_id, "-m", "side two")
        self.write_tree({**BASE, "a.txt": (REGULAR, b"side two\n")})
        self.snapshot()
        self.jj("new", side_one, "@", "-m", "conflicted merge")
        self.assertEqual(self.jj("log", "--no-graph", "-r", "@", "-T", "conflict").stdout, b"true")
        before = self.commit_id()
        conflict = self.jj("file", "show", "a.txt").stdout
        self.assertIn(b"<<<<<<<", conflict)
        # Force comparison against a resolved side: a merge's own parent-tree
        # already contains the conflict, so ordinary diffedit would be empty.
        self.edit("diffedit", "--from", side_one, "--to", "@", input=b"A\n")
        self.assertEqual(self.commit_id(), before)
        self.assertEqual(self.jj("file", "show", "a.txt").stdout, conflict)
        result = self.edit("diffedit", "--from", side_one, "--to", "@", input=b"Q\n", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.commit_id(), before)
        self.assertEqual(self.jj("file", "show", "a.txt").stdout, conflict)


class TwoDirectoryTests(WorkflowTests, RepositoryCase):
    pass


class ThreeDirectoryTests(WorkflowTests, RepositoryCase):
    three_way = True


class TwoDirectoryWithoutInstructionsTests(WorkflowTests, RepositoryCase):
    instructions = False


class ThreeDirectoryWithoutInstructionsTests(WorkflowTests, RepositoryCase):
    three_way = True
    instructions = False


class AdditionalTests(RepositoryCase):
    def test_command_scoped_split_context_and_alias(self):
        self.configure(instructions=False)
        with self.config.open("a") as config:
            config.write(
                '\n[aliases]\n'
                'pick = ["split"]\n'
                '[[--scope]]\n'
                '--when.commands = ["split"]\n'
                '[--scope.merge-tools.jj-patch-interactive]\n'
                'edit-args = ["--context", "split", "$left", "$right"]\n'
            )

        # A command-specific override must not leak into other commands.
        result = self.edit("diffedit", input=b"A\n")
        self.assertIn(b"in the result?", result.stdout)
        self.assertNotIn(b"in the first change?", result.stdout)
        self.assert_tree(FULL, disk=True)
        self.assertEqual(self.commit_id(), self.start_id)

        # jj must match the expanded canonical command as well as its alias.
        for command in ("split", "pick"):
            with self.subTest(command=command):
                # Distinct messages avoid replaying an identical rewritten
                # commit immediately after undo (a jj backend collision).
                result = self.edit(command, "-m", f"selection via {command}")
                self.assertIn(b"in the first change?", result.stdout)
                self.assertNotIn(b"in the result?", result.stdout)
                self.assert_tree(FIRST, "@-")
                self.assert_tree(FULL, disk=True)
                self.jj("undo")
                self.assertEqual(self.commit_id(), self.start_id)

    def test_explicit_absorb_context_without_instructions(self):
        self.configure("absorb", instructions=False)
        result = self.edit("absorb", "-i")
        self.assertIn(b"for absorption into ancestors?", result.stdout)
        self.assertNotIn(b"in the result?", result.stdout)
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_all_context_overrides(self):
        # Q makes each command reusable without undo and proves every spelling
        # accepts the interface; success paths are checked separately below.
        for mode in ("auto", "split", "diffedit", "squash", "restore", "commit", "absorb", "generic"):
            for three_way in (False, True):
                with self.subTest(context=mode, three_way=three_way):
                    self.configure(mode, three_way=three_way, instructions=False)
                    self.assert_cancelled(["diffedit"], b"Q\n")
                    self.edit("diffedit", input=b"A\n")
                    self.assert_tree(FULL, disk=True)
                    self.assertEqual(self.commit_id(), self.start_id)

    def test_split_relocation_flags(self):
        for flag in ("--onto", "--insert-after", "--insert-before"):
            with self.subTest(flag=flag):
                target = "@" if flag == "--insert-before" else "@-"
                self.edit("split", "-i", flag, target, "-m", "selected")
                self.assert_tree(FIRST, 'description(substring:"selected")')
                if flag == "--onto":
                    self.assert_tree(SECOND, disk=True)
                    self.assertEqual(self.parents(), [self.base_id])
                else:
                    self.assert_tree(FULL, disk=True)
                    self.assertEqual(self.parents(), [self.commit_id('description(substring:"selected")')])
                self.jj("undo")
                self.assertEqual(self.commit_id(), self.start_id)

    def test_squash_relocation_flags(self):
        for flag in ("--onto", "--insert-after", "--insert-before"):
            with self.subTest(flag=flag):
                target = "@" if flag == "--insert-before" else "@-"
                self.edit("squash", "-i", flag, target, "-m", "selected")
                self.assert_tree(FIRST, 'description(substring:"selected")')
                self.assert_tree(SECOND if flag == "--onto" else FULL, disk=True)
                self.jj("undo")
                self.assertEqual(self.commit_id(), self.start_id)

    def test_squash_multiple_sources_invokes_editor_for_each(self):
        # Feed one selection per invocation, rather than A which consumes all
        # changes of only the first invocation. Both source trees are one-hunk.
        self.write_tree(FIRST)
        self.snapshot()
        source_one = self.commit_id()
        self.jj("new", "-m", "second source")
        self.write_tree(FULL)
        self.snapshot()
        self.edit("squash", "-i", "--from", source_one, "--from", "@", "--into", self.base_id,
                  "--keep-emptied", "-m", "combined", input=b"y\nn\n")
        self.assert_tree(FIRST, 'description(substring:"combined")')
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)

    def test_absorb_unassignable_added_file_remains_in_source(self):
        expected = {**FULL, "new-file": (REGULAR, b"not owned by any ancestor\n")}
        self.write_tree(expected)
        self.snapshot()
        self.edit("absorb", "-i", input=b"A\n")
        self.assert_tree(FULL, "@-")
        self.assert_tree(expected, disk=True)

    def test_empty_diff_does_not_need_input(self):
        self.write_tree(BASE)
        self.snapshot()
        before = self.commit_id()
        self.edit("diffedit", input=b"")
        self.assert_tree(BASE, disk=True)
        self.assertEqual(self.commit_id(), before)

    def _pty_edit(self, args, replies):
        before = self.safety_state()
        pid, fd = pty.fork()
        if pid == 0:
            try:
                os.chdir(self.repo)
                os.execvpe(JJ, [JJ, "--no-pager", *args], self.env)
            except BaseException:
                os._exit(127)
        transcript = bytearray()
        sent = 0
        scanned = 0
        status = None
        deadline = time.monotonic() + TIMEOUT
        prompt = re.compile(rb"\[[^\r\n]*y,n,[^\r\n]*\] ")
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([fd], [], [], 0.1)
                if ready:
                    try:
                        chunk = os.read(fd, 65536)
                    except OSError as exc:
                        if exc.errno != errno.EIO:
                            raise
                        chunk = b""
                    transcript.extend(chunk)
                    # Reply only after the next whole prompt; never enqueue a
                    # second editor invocation's input in the first one's buffer.
                    match = prompt.search(transcript, scanned)
                    if match:
                        self.assertLess(sent, len(replies), bytes(transcript))
                        os.write(fd, replies[sent])
                        sent += 1
                        scanned = match.end()
                done, child_status = os.waitpid(pid, os.WNOHANG)
                if done:
                    status = child_status
                    break
            self.assertIsNotNone(status, f"PTY timeout; transcript={bytes(transcript)!r}")
            self.assertEqual(sent, len(replies), f"Missing prompt: {bytes(transcript)!r}")
            self.assertEqual(os.waitstatus_to_exitcode(status), 0, bytes(transcript))
        finally:
            if status is None:
                try:
                    os.killpg(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                os.waitpid(pid, 0)
            os.close(fd)
        self.assertEqual(self.safety_state(), before)
        return bytes(transcript)

    @unittest.skipUnless(os.name == "posix", "requires a POSIX PTY")
    def test_interactive_pty_waits_for_prompt_then_accepts(self):
        self._pty_edit(["diffedit"], [b"A\n"])
        self.assert_tree(FULL, disk=True)
        self.assertEqual(self.commit_id(), self.start_id)

    @unittest.skipUnless(os.name == "posix", "requires a POSIX PTY")
    def test_squash_multiple_sources_with_interactive_pty(self):
        self.write_tree(FIRST)
        self.snapshot()
        source_one = self.commit_id()
        self.jj("new", "-m", "second source")
        self.write_tree(FULL)
        self.snapshot()
        self._pty_edit(["squash", "-i", "--from", source_one, "--from", "@",
                        "--into", self.base_id, "--keep-emptied", "-m", "combined"],
                       [b"y\n", b"n\n"])
        self.assert_tree(FIRST, 'description(substring:"combined")')
        self.assert_tree(FIRST, "@-")
        self.assert_tree(FULL, disk=True)


class ColocatedTests(RepositoryCase):
    colocated = True

    def test_diffedit_does_not_stage_in_caller_index(self):
        # jj's colocated index represents @-. A diffedit of @ should neither
        # stage selected hunks there nor create private diff files in the repo.
        before = (self.gitdir / "index").read_bytes()
        self.edit("diffedit")
        self.assert_tree(FIRST, disk=True)
        self.assertEqual((self.gitdir / "index").read_bytes(), before)

    def test_abort_preserves_caller_index_and_git_refs(self):
        refs = self.git("for-each-ref")
        head = (self.gitdir / "HEAD").read_bytes()
        self.assert_cancelled(["diffedit"], b"y\nQ\n")
        self.assertEqual(self.git("for-each-ref"), refs)
        self.assertEqual((self.gitdir / "HEAD").read_bytes(), head)


if __name__ == "__main__":
    unittest.main(verbosity=2)
