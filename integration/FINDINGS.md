# Verification

Tests use jj **0.45.1** (`7c41cdeb16b6b321c64e789a966b6adf723816a5`).
The [workflow](../.github/workflows/test.yml) runs Go race tests, vet, a build,
and the full real-jj suite natively on Linux and macOS.

On September 28, 2026, [CI run 36431283979](https://github.com/filippo-agent/jj-patch/actions/runs/36431283979)
passed on both platforms at `4f1fae892e110b19d7e20743aff226a555a20f73`:
**204 integration tests per platform**, including both PTY cases and the
four-way directory/instruction matrix. The macOS run used arm64, Go 1.24.13,
and Python 3.14.7; its integration suite took 237 seconds.

Four subsequent regression cases exercise a jj protocol collision when deleting
a real `JJ-INSTRUCTIONS` file. They verify safe rejection with generated help
enabled, then successful accept/reject choices with instructions disabled.
The suite now contains 208 tests; see subsequent workflow runs for those results.

Earlier failures exposed missing commit/absorb instruction recognition and stdin
read-ahead across repeated squash editor invocations. Regression cases cover both.
The suite also verifies that command-scoped split prompts work through aliases
without changing the prompt used by diffedit.

Engine tests include deterministic randomized full/none and split-subset oracles.
A local 15-second parser fuzz run completed over one million executions without
failure. Native macOS testing exposed APFS's refusal to create invalid UTF-8
filenames; those names are tested on Linux. Both platforms test non-UTF-8 file
contents, binary data, and other unusual representable names.

See [README.md](README.md) for the source-pinned command inventory, assertions,
and coverage limits. No Windows support, universal filesystem certification,
or arbitrary partial conflict-resolution guarantee is implied.
