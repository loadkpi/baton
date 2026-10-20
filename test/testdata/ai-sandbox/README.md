# ai-sandbox fixture

A copy of the launcher of [loadkpi/ai-sandbox](https://github.com/loadkpi/ai-sandbox)
(MIT, see `LICENSE`), taken from the commit `ddaec7d06106b94f64fc128375e5e3b9bc9ca5da`
of its `main` branch:

- `.ai-sandbox/run.sh`, the launcher that renders the Codex configuration and
  prints the `docker run` invocation;
- `.ai-sandbox/versions.env`, the one file `run.sh` sources;
- `LICENSE`.

The tests copy these into a temporary Project as `.ai-sandbox/` and run
`run.sh` there with `AI_SANDBOX_PRINT_ARGS=1`, which prints the invocation
instead of running it, with a `docker` stub first on `PATH`. They show what
`run.sh` regenerates and that `baton sync` leaves it alone. Nothing here is ever
run outside the tests.

The copy is not edited. To refresh it, copy the same files from a newer commit
of `ai-sandbox` and update the commit above.
