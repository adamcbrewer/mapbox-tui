# Contributing

Use Go meeting the minimum in [go.mod](go.mod), Make and Python 3.11 or newer. Run
`make build` from the repository root. See [development notes](docs/DEVELOPMENT.md)
and [Go style](docs/GO_STYLE.md).

Keep PRs small and focused. Link the related issue, explain what changed and why,
and list the checks you ran. Run `make test` for offline checks; it disables
Mapbox credentials automatically. After viewer changes, also run
`make interactions`; include a terminal capture when it helps explain a visible change.

Use [GitHub issues](https://github.com/adamcbrewer/mapbox-tui/issues) to report bugs
or propose features. Include reproduction steps for bugs and a clear use case
for features. Use synthetic or minimised sample files, and remove credentials
and personal location data from files, logs and captures.

Contributions are submitted under the [MIT licence](LICENSE).
Contributors retain their copyright.

## Pull request titles

Use `type(scope): Description`, with a short description of what the change does
in third-person present tense. Start it with a capital letter:

```text
feat(journeys): Adds route planning
feature(journeys): Adds route planning
fix(imports): Preserves gaps between recorded segments
docs(releases): Explains how to publish a Linux release
ci: Checks pull request titles
```

Use `feat`, `feature`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`,
`chore` or `revert`. We accept `feature` as an alias for `feat`. The scope is
optional; when useful, choose a short lowercase area such
as `journeys`, `imports`, `viewer`, `mcp` or `releases`. Digits and hyphens are
allowed in scopes. Add `!` before the colon for a breaking change, for example
`feat(mcp)!: Changes workspace selection arguments`, and explain the migration
in the PR description.

The `PR title` Actions check validates the type, optional scope, punctuation and
capitalised description. Reviewers check that the wording is useful and describes
the change accurately. Edit the title to rerun the check. Individual commits don't
need this format; use the PR title when squash merging.

Release versions are chosen manually. See the [release reference](docs/RELEASING.md)
for version rules and step-by-step maintainer instructions.
