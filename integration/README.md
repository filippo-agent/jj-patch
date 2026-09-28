# Real jj integration suite

```sh
# From the repository root, once the new binary has been built:
python3 -m unittest discover -s integration -v
# Or test a separately built executable:
JJ_PATCH_INTERACTIVE_BIN=/absolute/path/to/jj-patch-interactive \
JJ_BIN=/path/to/jj \
python3 -m unittest discover -s integration -v
# A narrow smoke test:
python3 -m unittest integration.test_jj_patch_interactive.TwoDirectoryTests.test_diffedit_partial -v
```

Python's standard library only; requires real `jj`, `git`, and a built `jj-patch-interactive`.
`JJ_PATCH_INTERACTIVE_BIN` defaults to the repository's `./jj-patch-interactive`, independent of cwd.
`JJ_BIN` defaults to `jj` resolved on PATH.
`JJ_PATCH_INTERACTIVE_TEST_TIMEOUT` overrides the per-process/PTY timeout (30 seconds).
A missing binary is an error, **not a skip**. Tests do not build a moving source
checkout and do not edit the implementation. No fake diff editor is substituted.

Each test owns a temporary HOME, XDG directories, jj config, and a Git-backed jj
repository (non-colocated in the main matrix; colocated in two dedicated cases).
Ambient `JJ_*`/`GIT_*` configuration overrides are
removed. Git's system/global config is disabled. The user's config is never read
by the harness or edited. The test tool config is:

```toml
[ui]
diff-editor = "jj-patch-interactive"
[merge-tools.jj-patch-interactive]
program = "/absolute/path/to/jj-patch-interactive"
edit-invocation-mode = "dir"
edit-args = ["$left", "$right"]
# Three-directory variant:
# edit-args = ["--output", "$output", "$left", "$right"]
```

The jj 0.45.1 setting is **`edit-invocation-mode`**, not `invocation-mode`.
Instructions are left at jj's default in two classes; two further classes set
**`ui.diff-instructions = false`**. There is no `edit-instructions` setting in
this jj source. Explicit context tests prepend `--context MODE` to `edit-args`.

### Tested command-scoped context override

jj 0.45.1 supports limiting a context override to one command rather than
globally forcing the split prompt for every workflow:

```toml
[[--scope]]
--when.commands = ["split"]
[--scope.merge-tools.jj-patch-interactive]
edit-args = ["--context", "split", "$left", "$right"]
```

With `ui.diff-instructions = false`, the regression test checks that both
`jj split` and an alias configured as `pick = ["split"]` prompt with
`in the first change`, while `jj diffedit` still uses `in the result`.
It also asserts the selected/remainder trees, not only the wording. A separate
explicit `--context absorb` test checks `for absorption into ancestors` and
the resulting absorbed/source trees.

## Source-audited invocation inventory

Audited [jj source pinned at `35dbc3623b6dd5a23daf5b5ca09b8640694a72ae`](https://github.com/jj-vcs/jj/tree/35dbc3623b6dd5a23daf5b5ca09b8640694a72ae/cli/src).
The tested binary reports
`jj 0.45.1-7c41cdeb16b6b321c64e789a966b6adf723816a5`. Grepping all callers of
`diff_selector(` and `.diff_editor(` finds **six command families**, not just
`diffedit`/`split`:

| Command/source | External-editor entry points and selection semantics | Coverage |
|---|---|---|
| `commands/diffedit.rs` | Always opens editor; `-r`, `--from`/`--to`, filesets, `--restore-descendants`; selected tree replaces target tree | partial/all/none, range, non-WC revision, fileset, descendants |
| `commands/split.rs` | Interactive by default without filesets; `-i` or `--tool` enables with filesets; default sequential, `--parallel`, `-r`, relocation `--onto`/`--insert-after`/`--insert-before` | selected/remainder trees, parent topology, all/none, historical revision + fileset, all relocation flags |
| `commands/squash.rs` | `-i`/`--tool`; `-r`, `--from`/`--into`, filesets, `--keep-emptied`, relocation flags; **one editor invocation per source revision** | parent/source content, all/none (none errors), selectors, multi-source, all relocation flags |
| `commands/restore.rs` | `-i`/`--tool`; `--changes-in`, `--from`/`--into`, filesets, descendants; left=destination, right=restoration source (reverse of ordinary diffedit) | reverse partial selection, all/none, range/fileset/historical target |
| `commands/commit.rs` | `-i`/`--tool`, filesets; selected changes remain in old commit, remainder in new working-copy child; no `-r` | selected/child trees, all/none, fileset |
| `commands/absorb.rs` | **Supports `-i`/`--tool`** in 0.45.1; `--from`/`--into`, filesets; selected changes are candidates, not guaranteed absorbable | annotation-owned hunks move, unassignable addition stays, selectors, all/none |

`cli_util.rs::DiffSelector::select` first limits the candidate tree to the
fileset, leaves excluded paths on the left/base side, and can skip invoking the
editor for an empty diff. `merge_tools/mod.rs::DiffEditor::edit` routes all six
families to `merge_tools/external.rs::edit_diff_external`. That function chooses
two or three directories based on the presence of `$output`, inherits stdin,
and rejects a nonzero editor status before snapshotting results.

`merge_tools/diff_working_copies.rs` makes left read-only, and also right
read-only in three-directory mode; it writes `JJ-INSTRUCTIONS` to output and
adds a read-only-side preface in three-directory mode. Tests consequently use
the actual jj transport, not manually constructed substitutes.

Other commands such as `resolve` invoke **merge** editors (file arguments and a
different protocol); `diff`/`show` invoke read-only diff generators. `arrange`
does not use this external diff-editor entry point. They are not jj-patch-interactive
directory-diff-editor workflows. Noninteractive fileset selection by itself
does not invoke an editor except `diffedit`.

## Matrix and assertions

Every `WorkflowTests` case runs in all four combinations:

| Transport | Default instructions | Instructions disabled |
|---|---|---|
| `$left $right` | `TwoDirectoryTests` | `TwoDirectoryWithoutInstructionsTests` |
| `--output $output $left $right` | `ThreeDirectoryTests` | `ThreeDirectoryWithoutInstructionsTests` |

- `y`/`n` partial selection, reject-all, `A` accept-all, `q` with and without
  prior selections (all changes start **unselected**).
- `Q`, immediate EOF, `y` then `Q`, and `y` then EOF for all six families and
  parallel split: nonzero status, unchanged working-copy commit ID, entire
  visible revision graph, revision tree, and disk contents.
- `--tool jj-patch-interactive` implies interactive operation even without `-i`.
- Added-only changes: nested paths, empty file, NUL/non-UTF8 binary, executable,
  symlink and dangling symlink, whitespace/tab/Unicode filename, no final LF.
  Rejecting added-only changes must remove every added file from the result.
- Deletions both ways, executable-bit-only changes both ways, text-to-symlink
  replacement, rejected binary modification.
- Two separate hunks in one file, `s`, `S`, `G REGEX`, no-match filter, and
  autosplit + filter + `A` select actual byte-level subsets. Filtered `A` saves
  immediately without an extra `q`; no-match `G` followed by `q` saves an empty
  selection. Premature EOF is still an abort, not an implicit save.
- Real merge conflict materialization: accept-all preserves conflict/commit;
  abort preserves conflict. Compare explicit resolved-side `--from`, since a
  merge compared with its merged-parent tree can have no changes.
- Empty `diffedit` succeeds without input.
- Tab-indented XML and Makefile recipes render with real tabs, not `\t`
  escapes; selected file contents remain byte-for-byte identical.
- Terminal-hostile text (without NUL) is accepted and rejected in all four
  transports with exact blob/disk byte comparisons. Invalid UTF-8, CR/BEL/DEL,
  raw and UTF-8 C1, default-ignorable Unicode, and OSC clipboard/hyperlink,
  color, and cursor commands must become visible escapes. Ordinary Chinese
  and printable Cyrillic/Greek confusables remain their original glyphs.
- Deletion of a real `JJ-INSTRUCTIONS`: generated-help collisions fail safely;
  disabling jj's instructions permits both accepting and rejecting the deletion.

Additional tests cover all eight explicit contexts (`auto`, `split`,
`diffedit`, `squash`, `restore`, `commit`, `absorb`, `generic`) in both transports with
instructions disabled, split/squash relocation flags, repeated squash source
invocations, an unassignable absorb addition, and two **controlling-terminal PTY**
cases (`pty.fork`). One waits for the displayed prompt before sending `A`,
then checks exit status and unchanged full tree; the other paces replies across
two separate editor invocations in a multi-source squash. A separate piped
multi-source test detects an editor accidentally reading ahead into the next
invocation's input. Other cases use
`subprocess.run(input=...)` to exercise redirected stdin and EOF.

Dedicated terminal-safety PTY cases unset `NO_COLOR` and set `TERM=xterm` to
check black-on-yellow confusable glyphs, white-on-red generated escapes, and
restoration of added-line green after a warning. Only the exact renderer SGR
allowlist and TTY CR/LF/tab layout controls are permitted. A second PTY case
checks that both nonempty and empty `NO_COLOR` suppress all ANSI without
losing readable glyphs or visible escapes. Replies wait for the prompt; the
hostile fixture is the only changed file, so `A` cannot bypass its display.

Assertions compare entire trees as `{path: (Git mode, raw blob bytes)}` using
read-only `git ls-tree`/`cat-file` against jj-selected revisions, not just exit
codes or terminal messages. Disk comparisons use `lstat` semantics for
symlinks and check executable bits. The initial base object, caller Git index
(absence included), repository Git config, and test jj config must stay
unchanged across every editor invocation; a non-colocated caller `.git` may
not appear. A real pre-existing private index is also populated and compared,
and dedicated colocated cases check that diffedit does not stage hunks or alter
Git refs/HEAD on abort.
Expected jj commit rewrites are separately asserted. Generated `JJ-INSTRUCTIONS` must
never become a tracked path; a deliberately tracked user file with that same
name must remain selectable and must not be mistaken for metadata. Conflicted trees are checked through jj's own
conflict materialization instead of treating jj's conflict backing objects as
ordinary Git blobs.

## Deliberate limits

- Linux/macOS symlink, executable and PTY behavior; no Windows claim.
- No file-by-file mode (the tool's interface under test is directory mode),
  merge-editor protocol, Git submodules, filesystem races, disk-full failures,
  hardlink/xattr preservation, or extremely large files.
- Conflict coverage is accept-all and cancellation, not arbitrary partial
  conflict-marker surgery or conflict resolution UI.
- The general context matrix tests interface acceptance, success and
  cancellation. Commit, scoped split/generic, and explicit absorb cases assert
  human-facing prompt phrases; the rest do not depend on exact prose.
  Auto context is exercised through real jj
  instructions; instruction-free behavior has the same selection semantics.
- Relocation/multi-source topology and PTY tests run in the default transport;
  every normal family and file-edge case runs in the four-way matrix.
- Tests do not assert byte-for-byte immutability of `.jj` internals: jj itself
  legitimately snapshots and creates operations/objects. They assert visible
  graph/tree stability on cancellation and untouched caller Git index/config.
- They do not certify the supplied jj is the newest upstream release; the
  suite targets the locally supplied 0.45.1 binary and source.

## Recorded execution

See [FINDINGS.md](FINDINGS.md) for native Linux/macOS runs and their source
revisions. Counts include unittest methods; cancellation/context matrices also
contain additional subtests.
