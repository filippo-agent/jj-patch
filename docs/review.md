# Upstream review and jj diff-editor contract

**Research date: 2026-09-28.** This report separates upstream dispositions,
observations of this implementation, and deferred work. It does not claim that
an upstream open proposal has merged or that every test/platform has passed.

## Scope and source pins

Read both trackers' open **and closed** issues/PRs, bodies, issue comments, PR
reviews/inline comments, and relevant diffs. Dependency-only updates were included
in the inventory but not mistaken for UI fixes.

| Source | Audited revision | Coverage |
|---|---|---|
| [cwarden/git-add--interactive](https://github.com/cwarden/git-add--interactive) | [`03992e245cbef929e1ad7c30a6d8f43e89647e9d`](https://github.com/cwarden/git-add--interactive/tree/03992e245cbef929e1ad7c30a6d8f43e89647e9d) | All 12 entries: 8 issues, 4 PRs; 7 issue comments; no PR reviews/inline comments. |
| [arxanas/scm-record](https://github.com/arxanas/scm-record) | [`7654fdcbeb1144f3f0790b200bc6a3a5d33c4571`](https://github.com/arxanas/scm-record/tree/7654fdcbeb1144f3f0790b200bc6a3a5d33c4571) | 163 accessible entries: 34 issues, 129 PRs; 213 issue comments, 81 reviews, 101 inline comments. |
| jj **v0.45.1** | [`7c41cdeb16b6b321c64e789a966b6adf723816a5`](https://github.com/jj-vcs/jj/tree/7c41cdeb16b6b321c64e789a966b6adf723816a5) | Release contract and command implementations; integration suite targets this release. |
| jj audited `main` | [`35dbc3623b6dd5a23daf5b5ca09b8640694a72ae`](https://github.com/jj-vcs/jj/tree/35dbc3623b6dd5a23daf5b5ca09b8640694a72ae) | All current diff-editor call sites, invocation/snapshot code, related tests/docs; not the entire jj tracker. |

The scm issues listing omitted #147; the separate pulls listing and direct GET
recovered it. #24/#63/#160 were absent from both listings and returned 404.
[The scm inventory](scm-audit.md) covers every accessible number through #166,
including 90 merged, 30 closed-unmerged and 9 open PRs. “Closed” alone never means
“fixed”; an open PR's synthetic merge SHA is not a merged commit.

## Original 12 cwarden issues and PRs

Local dispositions below were checked against [the engine](../internal/edit/),
[prompt](../internal/prompt/), [CLI](../main.go), and their tests. This project
replaces the Git-index mutation backend rather than claiming to merge all
original PRs unchanged.

| # | Upstream disposition | Disposition here |
|---|---|---|
| [1](https://github.com/cwarden/git-add--interactive/issues/1) Undo | **Open.** Undo accidental decisions, including `d`. | **Partial:** `J/K` and `g` revisit decided hunks across files while the selection session remains active. No dedicated undo/history command; session abort discards all choices. |
| [2](https://github.com/cwarden/git-add--interactive/issues/2) Single key | **Open.** Honor `interactive.singleKey` or equivalent. | **Explicitly unimplemented/deferred.** Commands require Enter. Neither raw single-key mode nor Git's configuration setting is supported. |
| [3](https://github.com/cwarden/git-add--interactive/issues/3) jj editor | **Open.** Requests a native jj diff-editor; links a temporary-Git wrapper. | Directory snapshots, optional third output, explicit context override, and six-context instruction recognition replace real-index staging. Git is used only for isolated `diff --no-index`, not commits or index mutation. |
| [4](https://github.com/cwarden/git-add--interactive/issues/4) `go install` | **Open.** Avoid requiring a source clone. | Go main package remains installable through normal Go tooling. This audit does not claim a published release/tag or verify remote installation. |
| [5](https://github.com/cwarden/git-add--interactive/issues/5) Git does not pick it up | **Open.** Git 2.48.1 invokes its builtin despite the documented exec-path setup; aliases/shims discussed. | Deliberately **not** a replacement for Git's builtin. Configure jj to invoke this editor directly; no Git binary wrapper. |
| [6](https://github.com/cwarden/git-add--interactive/issues/6) Checkout while staging | **Open.** Discard a debug hunk while reviewing staging. | Not implemented as a mixed destructive action. Here `n` excludes a delta from the result; whether it stays elsewhere or is discarded depends on the jj command. Use `jj restore -i` for restoration. |
| [7](https://github.com/cwarden/git-add--interactive/pull/7) Empty context editing | **Closed-unmerged**, superseded by #9. | Edited patch processing preserves whitespace-only context. An empty/comments-only edit cancels that edit, not the session. |
| [8](https://github.com/cwarden/git-add--interactive/issues/8) J/K navigation | **Open.** Revisit decided hunks, ideally across files. Original dispatch lowercases J/K; even suggested `g` is caught by its G test. | Case-sensitive `j/k` skip to undecided visible hunks; `J/K` include decided ones; `g` chooses a visible hunk. Traversal spans files, with no endpoint wrap. |
| [9](https://github.com/cwarden/git-add--interactive/pull/9) Hunk edit fixes | **Open**, head [`21e859aac2`](https://github.com/cwarden/git-add--interactive/commit/21e859aac239c2dac7f90f47c9bda1222f3c6acb): preserve blank context and add `--recount`. | Equivalent semantics implemented differently: preserve patch bytes, parse/recount headers, validate exact original old-line span/context, then apply operations. Metadata/binary/symlink edits rejected; invalid edits leave the selection unchanged. |
| [10](https://github.com/cwarden/git-add--interactive/pull/10) Sort files | **Open**, head [`46c3a36385`](https://github.com/cwarden/git-add--interactive/commit/46c3a36385c502702d890e72639fe5c84f96547b): sort modified-map keys. | Engine sorts the union of left/right paths, including additions. No randomized map display order. |
| [11](https://github.com/cwarden/git-add--interactive/pull/11) Sort all lists | **Open**, head [`8872f98a4b`](https://github.com/cwarden/git-add--interactive/commit/8872f98a4b3d0ae1890d470e463bef0ad03ab5b8): also sort untracked list missed by #10. | Same sorted snapshot-path model covers additions; no separate Git untracked list to overlook. |
| [12](https://github.com/cwarden/git-add--interactive/issues/12) Grep misses | **Closed issue**, reporter had entered literal quote marks. No upstream code fix. | Regex arguments are not shell-unquoted. Filters are a view, not deletion of hidden decisions. Invalid regex preserves the previous filter; blank `G` clears it. |

**Accept/cancel and input:** `q` saves decisions and rejects remaining undecided
hunks; `Q` or premature EOF aborts without publication. `A` accepts remaining
visible hunks and saves; hidden undecided hunks are not selected. This distinction
is intentional, not inherited accidentally from Git. A discovered buffered-stdin
bug consumed replies intended for the next editor in multi-source squash.
Current prompt code limits command reads to avoid prefetching future input;
`TestCommandsDoNotConsumeFutureInput` and `TestMultipleInvocationsShareInput`
cover the fix. The real multi-source regression remains in the integration suite;
these observations are not a claim about an older binary's results.

## jj's directory-edit contract

Primary release sources: [tool selection](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/merge_tools/mod.rs#L235-L325),
[external invocation](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/merge_tools/external.rs#L384-L444),
[working copies and instructions](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/merge_tools/diff_working_copies.rs).
The audited main has the same operation/output contract; relevant subsequent
changes include native `OsString` path interpolation and split's description-editor
flow, not new operation metadata ([main invocation](https://github.com/jj-vcs/jj/blob/35dbc3623b6dd5a23daf5b5ca09b8640694a72ae/cli/src/merge_tools/external.rs#L382-L442)).

- `--tool` overrides `ui.diff-editor` and implies interactive selection where
  applicable. Default external edit arguments are `[$left, $right]`.
- jj directly executes the configured program; there is **no implicit shell**,
  no edit-specific cwd change, and no dedicated operation environment variable.
  Inherited cwd is not reliable repository identity, especially with `-R`.
- `$left` is before; `$right` starts as after. Only changes matching the fileset
  are materialized in sparse temporary directories, not a complete checkout.
  Two-way editing reads back **right**. Mentioning `$output` activates three-way
  editing: output starts as a copy of right, and **output alone** is read back.
  This is not a three-commit merge; `$base` is not provided.
- jj waits for exit. **Zero accepts the output snapshot; nonzero aborts.** There
  is no stdout patch protocol or edit-specific accepted-exit-code list. Merely
  returning success without editing keeps every initial candidate change.
- Selector-based commands first preserve unmatched paths from left and skip an
  empty candidate diff. Completely new paths outside the compared sparse set
  may be ignored by jj even if created by an editor.
- jj also supports `edit-invocation-mode="file-by-file"`; **jj-patch supports
  directory mode only**. External diff viewing and `jj resolve` use separate
  `diff-args`/`merge-args` contracts, not this one.

### All six command families

“Selected” means the left-to-result delta, never a Git-index operation. Parent
means the merged parent tree where a revision has multiple parents.

| Command / release source | Before → candidate | Meaning of selection |
|---|---|---|
| [`diffedit`](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/commands/diffedit.rs#L91-L170) | Target's parents → target, or explicit `--from` → `--to` | Selected changes form the edited target. No edit to external output is a no-op; selecting no hunks in this checkbox/prompt model instead restores before. |
| [`split`](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/commands/split.rs#L548-L605) | Target's parents → target | Default selected first change retains original identity; remainder becomes child. `--parallel` makes siblings. Relocation `--onto/-o`, `--insert-after/-A`, `--insert-before/-B` gives selected changes a new identity/location, retaining original identity for remainder. |
| [`commit -i`](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/commands/commit.rs#L83-L145) | Working-copy parents → working-copy commit | Selected changes finish the current commit; remainder becomes the new working copy. |
| [`squash -i`](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/commands/squash.rs#L485-L539) | Each source's parents → that source, **not destination** | Selected changes move to destination. Multiple sources can invoke the editor repeatedly on shared stdin. Destination can be arbitrary; “move to parent” is not generally correct. |
| [`restore -i`](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/commands/restore.rs#L103-L181) | **Destination current tree → restoration source** | Select restoration changes. Default source is target's parents; explicit `--from/--into` changes that. Selecting everything restores toward source, rather than preserving destination changes. |
| [`absorb -i`](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/cli/src/commands/absorb.rs#L95-L163) | Source's parents → source | Select candidates for annotation-based ancestor assignment. Ambiguous/unassignable hunks remain source; accept-all does not guarantee everything is absorbed. Empty selection is a jj error. |

`split` is interactive by default without filesets; the other selectable commands
use `-i` or `--tool` (diffedit always edits). Variants may alter subsequent graph
rewrites without changing the snapshots or instruction text.

### Context detection: useful, never omniscient

`JJ-INSTRUCTIONS` is optional human prose, not an authenticated or versioned
operation API. It can be disabled by `ui.diff-instructions=false`; an existing
file with that name prevents synthetic creation. In three-way mode the output
and right messages have different preambles. jj removes its own generated output
message before snapshotting, but genuine user files must be preserved.

[The implementation](../internal/edit/instructions.go) recognizes known prose
only for an eligible root regular file absent from left, preserves generated
output instructions byte-for-byte, and treats unknown formats as data. For an
added user file deliberately imitating jj's generated text, `--no-instructions`
disables recognition (pair it with `ui.diff-instructions=false`).
[Prompt context](../main.go) uses anchored family prefixes and a neutral
fallback; `--context` explicitly overrides the wording. All six families,
including absorb, have recognition paths. This does **not** recover destination
IDs, absorb ownership, parallel/relocated split flags, or arbitrary revsets.
In particular, upstream split's generic “new commit” prose does not accurately
describe every identity/placement variant; prompts avoid claiming which original
change ID receives the selection.

For deterministic command-family labeling, jj's
[command-conditional config](https://github.com/jj-vcs/jj/blob/7c41cdeb16b6b321c64e789a966b6adf723816a5/docs/config.md)
can pass an explicit `--context` per canonical command. Conditions match command
names, not flags. Parent-process inspection, cwd, and temp directory names are
weaker heuristics. No automatic `$command`, `$revision`, `$destination` or
`$left_label` substitution exists. Also do not assume generic structured
`{env, command}` config applies here: the external-tool conversion extracts
name/arguments, not that environment map.

## scm-record lessons applied—or deliberately not claimed

**Not a repository move:** standalone `scm-diff-editor` lives in arxanas/scm-record
(extraction #53, consolidation #60). jj's builtin embeds **the scm-record library**
and computes its own diffs; installing/fixing standalone does not fix builtin.

| Upstream evidence | Lesson for this implementation |
|---|---|
| [#50](https://github.com/arxanas/scm-record/issues/50), [#78](https://github.com/arxanas/scm-record/issues/78), [#125](https://github.com/arxanas/scm-record/issues/125); open footer [#126](https://github.com/arxanas/scm-record/pull/126) | Explain selection meaning visibly, rather than mislabel every command as staging. Builtin discards jj's instruction closure. #126 proposes a status-message API but has no jj wiring; it is not a shipped solution. |
| [#25](https://github.com/arxanas/scm-record/issues/25), [#44](https://github.com/arxanas/scm-record/issues/44); merged [#91](https://github.com/arxanas/scm-record/pull/91), [#110](https://github.com/arxanas/scm-record/pull/110) | Keep help in the prompt, preserve keyboard-only use and predictable nonwrapping navigation. Open search/keybinding/menu proposals are not claimed implemented here. |
| [#26](https://github.com/arxanas/scm-record/issues/26), merged [#93](https://github.com/arxanas/scm-record/pull/93)/[#95](https://github.com/arxanas/scm-record/pull/95); open [#164](https://github.com/arxanas/scm-record/issues/164) | Creation/deletion/mode and content must remain consistent across every selection path. Here empty files, symlinks, binary/type changes have indivisible choices; ordinary content and executable-mode changes are handled deliberately. No claim to repair scm-record's invert-all bug upstream. |
| [#118](https://github.com/arxanas/scm-record/issues/118), jj [#6829](https://github.com/jj-vcs/jj/issues/6829); open [#158](https://github.com/arxanas/scm-record/pull/158) | Do not reconstruct partial replacements merely by appending checked lines. This engine validates edits against original content and applies positioned operations; overlapping/unrepresentable choices fail before publication. #158's caller-populated groups are not a general or enabled standalone fix. |
| [#83](https://github.com/arxanas/scm-record/issues/83), [#73](https://github.com/arxanas/scm-record/issues/73) | External hunk editing needs validation, not just an editor launch. This project edits text hunks; arbitrary metadata edits and multi-way commit creation/reordering remain outside scope. |
| [#57](https://github.com/arxanas/scm-record/issues/57), merged [#61](https://github.com/arxanas/scm-record/pull/61) | Terminal display must not corrupt stored bytes or execute embedded control sequences. Prompt rendering escapes unsafe bytes; patch processing preserves CRLF and no-final-newline semantics. |
| Closed-unmerged [#90](https://github.com/arxanas/scm-record/pull/90), jj [#5393](https://github.com/jj-vcs/jj/pull/5393) | Initial selection is part of UX semantics. Current builtin also starts unchecked; accepting an unchecked selection is not an external no-op. Here `q` means save and `Q`/EOF means abort, explicitly. |

### Publication and verification boundaries

[The engine](../internal/edit/session.go) snapshots before/after/output, computes
**before + selected delta**, validates all hunks/path hierarchy, checks output
against its initial snapshot, and builds a sibling staging directory before
publication. [Linux](../internal/edit/filesystem_linux.go) uses
`renameat2(RENAME_EXCHANGE)`;
[macOS](../internal/edit/filesystem_darwin.go) uses
`renameatx_np(RENAME_SWAP)`. Unsupported platforms/filesystems fail closed, not
by falling back to partly visible per-file writes. Descriptor-relative no-follow
walks avoid treating symlink targets as file contents. Atomic directory visibility
is **not** a claim of power-loss durability or exclusion of hostile concurrent
writers after the final validation check.

Source-inspected unit tests cover selection/edit/cancellation, CRLF, empty and
binary files, modes, symlinks, type/path collisions, Git-environment isolation,
output changes, safe terminal rendering and shared-input regression. The
[real-jj integration suite](../integration/test_jj_patch.py) covers all six
families, two/three-directory transports, instructions on/off, relocation,
multi-source squash, absorb's unassignable changes, and colocated-repository
safety. [CI](../.github/workflows/test.yml) defines Linux/macOS runs against pinned
jj v0.45.1. These are coverage/source observations: consult the actual test run
for pass/fail; an earlier 202-case run exposed the stdin regression above.

Remaining deliberate limits include **singleKey**, dedicated undo history,
custom key maps, file-by-file/merge-editor protocols, general multi-way commit
editing, and arbitrary metadata editing. Source review and tests do not establish
support for submodules, every filesystem failure/race, or large-file performance.
