# jj-patch

A `git add -p`-style diff editor for [Jujutsu](https://jj-vcs.dev/).
Walk through changes one hunk at a time, split them, search them, or edit a
patch in your editor. No staging area to manage.

## Install

Requires Linux or macOS, Go 1.24.2 or newer, and Git on your `PATH`.

```sh
go install github.com/filippo-agent/jj-patch@latest
```

Put Go's bin directory on your `PATH`, then add to your jj config:

```toml
[ui]
diff-editor = "jj-patch"
```

Use `jj split`, `jj diffedit`, `jj commit -i`, `jj squash -i`, or
`jj restore -i`, or `jj absorb -i` as usual.

## Picking changes

Nothing is selected initially. Answer **y** to include a change, **n** to
leave it out. For `jj split`, selected hunks go into the first change;
for `jj diffedit`, they are the changes to keep.

| Key | Action |
| --- | --- |
| `y` / `n` | Include / exclude this hunk |
| `a` / `d` | Include / exclude the remaining hunks in this file |
| `q` | Save selections so far; exclude undecided hunks |
| `Q` | Abort without saving |
| `s` / `S` | Split this hunk / enable automatic splitting |
| `e` | Edit this hunk using `$VISUAL` or `$EDITOR` |
| `j` / `k` | Next / previous undecided hunk |
| `J` / `K` | Next / previous hunk, including decided ones |
| `g` | Go to a hunk number |
| `/` | Search visible hunks and paths across files |
| `G` | Filter hunks by regex across files; empty pattern clears |
| `A` | Include remaining hunks matching the filter across files, then save |
| `?` | Help |

Press Enter after a command. EOF and Ctrl-C abort; unlike `q`, they don't
save a partial selection.

Added files—including empty files—are selectable. Binary files, symlinks,
and type changes are selected as whole changes. Renames appear as a deletion
and an addition. Git is used privately to calculate diffs, not to stage or
commit in your repository.

Prompts use jj's edit instructions when available: “Include this hunk in
the first change?” for splitting, for example. If instructions are disabled
or unrecognized, the prompt is generic. To set a split-specific prompt even
without instructions, use jj's command-scoped config:

```toml
[[--scope]]
--when.commands = ["split"]
[--scope.merge-tools.jj-patch]
edit-args = ["--context", "split", "$left", "$right"]
```

The accepted contexts are `auto` (default), `split`, `commit`, `diffedit`,
`squash`, `restore`, `absorb`, and `generic`. jj controls placement when using
`split --parallel` or relocation flags; “first change” means the selected change.

### Separate output directory

The default edits jj's right-hand temporary directory. Three-directory
editing is also supported:

```toml
[merge-tools.jj-patch]
program = "jj-patch"
edit-args = ["--output", "$output", "$left", "$right"]
```

Use directory invocation (jj's default), not file-by-file mode.

### Instruction-file collisions

Ordinary files named `JJ-INSTRUCTIONS` remain selectable. If you're adding one
whose contents imitate jj's generated instructions, disable the heuristic:

```toml
[ui]
diff-instructions = false
[merge-tools.jj-patch]
edit-args = ["--no-instructions", "$left", "$right"]
```

Command-scoped `--context` still works with instruction detection disabled.

## Development

```sh
make check       # Go tests, real jj integration tests, and go vet
```

The integration tests require `jj` on your `PATH`. See
[integration/README.md](integration/README.md) for the exercised command and
edge-case matrix, and [docs/review.md](docs/review.md) for upstream research.

## Credits

Forked from Christian G. Warden's
[git-add--interactive](https://github.com/cwarden/git-add--interactive),
a Go implementation of Git's interactive patch UI. MIT licensed; see
[LICENSE](LICENSE).
