# Verification

On September 28, 2026, [CI run 36432262103](https://github.com/filippo-agent/jj-patch/actions/runs/36432262103)
passed natively on **Linux and macOS** at
`1fc3627f1f6ddb466ec2d2dc12cc434aac391eb8`:

- `go test -race ./...`
- `go vet ./...`
- `go build -o jj-patch .`
- **208 real-jj integration tests on each platform**, including two controlling
  PTY cases and the complete two/three-directory × instructions-on/off matrix.

The tested jj release is **0.45.1**
(`7c41cdeb16b6b321c64e789a966b6adf723816a5`). CI uses Go 1.24.x;
Go 1.24.2 was also tested locally. The macOS runner is arm64.

Regression cases cover commit/absorb instruction recognition, stdin read-ahead
across repeated squash invocations, and the collision between a deleted real
`JJ-INSTRUCTIONS` file and jj's generated help. The latter fails safely with
help enabled and supports normal accept/reject choices after disabling it.
Command-scoped split prompts also work through aliases without changing the
prompt used by diffedit.

Engine tests include deterministic randomized full/none and split-subset oracles.
A local 15-second parser fuzz run completed over one million executions without
failure. Native macOS testing exposed APFS's refusal to create invalid UTF-8
filenames; those names are tested on Linux. Both platforms test non-UTF-8 file
contents, binary data, and other unusual representable names.

See [README.md](README.md) for the source-pinned command inventory, assertions,
and coverage limits. No Windows support, universal filesystem certification,
or arbitrary partial conflict-resolution guarantee is implied.
