# Integration execution

On September 28, 2026:

```text
python3 -m unittest discover -s integration -v
Ran 202 tests in 81.873s
OK
```

- jj: `0.45.1-7c41cdeb16b6b321c64e789a966b6adf723816a5`
- jj-patch: `v0.5.0+dirty 03992e245cbef929e1ad7c30a6d8f43e89647e9d`
- Tested binary SHA-256:
  `fa657bdf3e6d8ea297dd98c85e01c7a7421cbd8ad33365ad41d691f0ffdb1514`

All 202 tests passed, including four transport/instruction combinations, eight
explicit contexts, actual tree/mode/content assertions, cancellation/EOF,
existing private and colocated Git index checks, and two controlling-PTY tests.
The raw log is generated as `test-results.log` and ignored by Git.

Earlier failures exposed generated instruction detection for commit/absorb and
stdin read-ahead across multiple squash editor invocations. Both regressions
now pass. Filtered `A` is tested as an immediate save with no extra `q`.
No failures are hidden with expectedFailure or implementation-dependent skips.

The full source-pinned workflow inventory, tested features, and deliberate
coverage limits are in README.md. This is a Linux/POSIX execution result, not a
macOS/Windows certification. No implementation files were edited by this test
work, and no commit was made.
