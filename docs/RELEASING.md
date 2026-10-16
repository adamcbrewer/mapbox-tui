# Release reference

Use this when preparing a release. Releases currently contain one Linux x86-64
binary. You choose the version and publish the release; GitHub Actions runs the
checks and prepares a draft with an archive, checksum and generated notes.

The process uses the existing Makefile and GitHub CLI. There is no release service,
automatic version bump or package-manager publishing to maintain.

This follows GitHub's [tag-and-release model](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases):
tag a source revision, build it, and attach binaries to the release. Open-source
projects vary in how they automate those steps; this project's choices are manual
version selection, automated checks/builds and a draft reviewed before publishing.

Commit and push the source code, documentation and workflows. Built binaries and
archives are not committed: `bin/` and `dist/` are ignored by Git. Pushing a version
tag makes Actions build that source and upload the archive and checksum as GitHub
Release attachments. Publishing the draft makes those downloads available to users.

## Interactive release command

Use the Go version pinned in `go.mod`, on Linux x86-64, with Make, Python 3.11+,
GitHub CLI and the terminal dependencies listed in [Development](DEVELOPMENT.md).
Authenticate `gh` with permission to push tags and manage releases. From a clean
checkout on `main`, run:

```sh
make release
# Equivalent direct command:
go run ./cmd/release
```

This is the official maintainer workflow. It fetches origin without overwriting
tags and fast-forwards `main`. If that updates the checkout, it asks you to rerun
so the release tool is rebuilt from the selected source. It then inspects
local/remote tags and GitHub releases.
The initial screen suggests the next available version; edit it before starting.
Only one patch, minor or major increment from the highest published stable release
is accepted, with lower components reset. Existing tags and drafts reserve their
versions. For a repository with no stable releases, the initial version is `v0.1.0`.

The nine sections run the actual checks and commands:

1. Verify tools, clean source, release history and successful CI for the exact commit.
2. Run `make check` and `make interactions` with `NO_COLOR` unset for interactions.
3. Run `make package`, verify the checksum and archive contents, then check the
   extracted binary's version, commit and offline rendering.
4. Ask for `y`, recheck source and version availability, create an annotated tag
   and push only that tag. Existing tags are never moved or overwritten.
5. Find the release workflow for that tag and commit, then wait for its jobs.
6. Inspect the draft (archive at most 256 MiB; checksum at most 4 KiB),
   download attachments into a fresh directory, and verify
   the checksum and archive. Extraction is cancellable and limited to 256 MiB total,
   with at most 1 MiB per documentation file. Before any execution, compare every file's contents and permissions
   with the verified local archive, then check embedded metadata and offline
   rendering again. Packaging normalises Go flags, experiments, CPU baseline and workspace settings,
   ignoring persistent user Go configuration and automatic VCS metadata (the
   explicit version and commit remain embedded). Local and CI builds must use the same
   pinned Go toolchain; a content mismatch stops execution and publication.
7. Open the downloaded binary with the local fixture and `--no-provider`.
   Quit the viewer to return, then type `y` to confirm it worked.
8. Review the generated notes. Press `e` to edit, Ctrl+S to save, or Escape to
   discard edits. Type `y` to accept the notes. Empty notes stay in this step so
   you can correct them with `e`.
9. Save the accepted notes to the draft and show its GitHub link. Open the link,
   review the release in GitHub, then click **Publish release**. Return to the tool
   and type `y` to verify publication, notes, attachments and latest status.
   The tool never publishes automatically. If it is still a draft, publish it on
   GitHub and press Enter to retry. Leave a newer stable release marked as latest.

The checklist expands only the active section, showing nested command output and
a braille spinner. Successful sections collapse to one green summary line, and
upcoming sections stay hidden. New output stays in view automatically. Polling updates the same waiting leaf, with bounded retries and
backoff. Completed section headings turn green. The cyan/pink bar counts completed
sections, rather than estimating how long a server request will take. The output
under each operation retains its latest twelve lines. Arrow/page keys and the
mouse wheel scroll overflowing content; advancing a section brings it into view.
Confirmation prompts use `[y/N]`: enter `y` to proceed. Empty input or `n`
keeps the current step waiting; Escape cancels.

Version entry and confirmation prompts stay pinned at the bottom while the
checklist and full release notes scroll above. Enter submits the visible input. `NO_COLOR=1` disables colour. Short terminals show only the active step and its prompt, without a fixed
minimum-size requirement.

Escape or Ctrl+C cancels local commands and restores the terminal. It does not
cancel an already-triggered GitHub workflow. Recovery state is locked and written
atomically in the Git directory, without credentials. Resume from the same clean
source checkout with:

```sh
make release RELEASE_ARGS='--resume'
```

Live preflight requires repository write access for the active `gh` account.

Resume repeats verification and confirmations, reconciles an uncertain tag push
or publication, and downloads fresh attachments. If a workflow rerun failed after
creating a release, resume can inspect the matching release and continue through
full artifact verification. It does not recreate a draft or
move a tag. A changed source commit requires a new release version. Keep the
saved source checked out while resuming; a newer `main` on origin is accepted only
when the already-tagged source remains its ancestor. If GitHub reports altered
attachments, restart with `--resume` and review them again before publication.

Notes comparisons ignore LF versus CRLF line endings introduced by GitHub's web
editor. If the published text actually changes, the tool returns to notes review
and asks you to accept the current GitHub notes with `y`. You can edit published
notes on GitHub and press `r` to refresh them in the tool. This also works when
resuming an already published release. Accepting notes continues verification of
the release title, tag, attachments, prerelease flag and latest status; it does
not overwrite the published notes.

### Interactive tests and local rehearsal

There is one command and one workflow. To exercise its UI without credentials,
subprocesses or network access, explicitly select its fixture-backed test mode:

```sh
make release RELEASE_ARGS='--mode=test'
make release RELEASE_ARGS='--mode=test --scenario=delayed-workflow --delay=200ms'
```

The test mode uses the same steps, confirmations, version policy, asynchronous
result handling and rendering. Repository and commit values are fictional. It
substitutes deterministic operation results, including the viewer handoff and
publication. Scenarios are `success`, `slow-checks`, `delayed-workflow`,
`failed-checks`, `auth-error`, `checksum-mismatch`, `existing-draft`,
`existing-tag` and `changed-public-notes`. Enter retries a failed fixture. `--version` seeds the editable tag;
`--delay` controls fixture timing. There is no separate release-demo command or target.

To run real local checks, packaging and the offline viewer without creating a tag
or publishing, use a clean checkout and authenticated GitHub access:

```sh
make release RELEASE_ARGS='--mode=rehearsal'
```

Rehearsal reads repository/release metadata and fetches refs, but does not advance
your branch. It explicitly skips remote CI as a publication gate, tag push,
release workflow, draft download, notes and publication. It cannot be used as
proof that those skipped stages passed.

### Automated verification

```sh
make test
make release-interactions
```

The tests exercise the production adapter using a strict fake command runner,
including exact Git/GitHub arguments, confirmation ordering, archive verification,
rehearsal restrictions and recovery after lost mutation responses. They also cover
process cancellation, recovery-file locking, version guards, shared session
transitions and normal/narrow text snapshots. The tests never push a real tag or
publish a release.

The interaction suite executes the same release command in test mode inside tmux.
One happy path runs all nine sections, including delayed workflow discovery,
version input, confirmations and notes editing. One failure path checks the error,
retry and cancellation. A third path reviews, refreshes and explicitly accepts
changed published notes before completing verification. Collapsed output, a
six-row confirmation prompt and terminal restoration are also checked. It needs the terminal dependencies
from [Development](DEVELOPMENT.md). Text, SVG and PNG captures are saved under
`reports/generated/release/` and retained by CI. To intentionally update text
snapshots, run `UPDATE_RELEASE_SNAPSHOTS=1 go test ./internal/release` and review
the diff. The manual commands below remain useful for diagnosis and recovery.

## Choose a version

Start with `v0.1.0`. Use `vMAJOR.MINOR.PATCH` tags, with no version file to edit.
The tag is passed into the binary at build time. `mapbox-tui --version` shows the
version and commit; MCP reports the same version during initialisation.
Ordinary source builds report `dev`.

While we are on `v0.x.y`:

- Increment the patch for compatible fixes, for example `v0.1.0` to `v0.1.1`.
- Increment the minor and reset the patch for features or breaking changes,
  for example `v0.1.1` to `v0.2.0`.
- Describe breaking changes and migration steps in the release notes.

Treat CLI flags, JSON output, MCP tool arguments/results and saved configuration
as public interfaces. At `v1.0.0`, commit to keeping those interfaces compatible
within a major version: fixes increment patch, compatible features increment minor,
and breaking changes increment major. See [Semantic Versioning](https://semver.org/).

PR titles help you decide the version; they don't choose it automatically.
The workflow accepts only stable `vMAJOR.MINOR.PATCH` tags. Prerelease and nightly
channels can be added when there is a use for them.

## One-time repository setup

Merge the release, CI and PR-title workflows into `main` before tagging a release.
The PR-title workflow starts checking new and edited PRs once it is on `main`.

In GitHub repository settings:

1. Under **General → Pull Requests**, use squash merging and choose **Pull request
   title** as the default squash commit title. Check that title when merging.
2. After the `PR title` check has run, add it to the required status checks for
   `main`, alongside `Go checks`, `Race checks` and `Terminal interactions`.
   Keep the existing protection settings. This makes a failing title block merging.
3. Check that repository/organisation Actions policies permit the release job's
   `contents: write` permission. It uses the built-in `GITHUB_TOKEN`; no personal
   access token or repository secret is needed.

The title check reads PR metadata without checking out contributor code. It also
runs when a title is edited, including on fork PRs.

## Prepare and tag a release

Run these commands from a clean checkout on Linux x86-64. Use the Go compiler
pinned in [mise.toml](../mise.toml), matching the version selected from `go.mod`
by CI and release builds. Follow the [Go toolchain setup](DEVELOPMENT.md#go-toolchain)
for mise. You'll also need Make, Python 3.11 or newer, and the terminal-check
dependencies listed in [Development](DEVELOPMENT.md). Install and authenticate
GitHub CLI for `gh`. You need permission to push tags and manage releases.

1. Update `main`, review the changes since the last release, and choose the next
   version. Confirm the merged changes passed CI.

   ```sh
   git switch main
   git pull --ff-only
   git status --short
   gh release list
   ```

   Stop if the working tree has changes. For later releases, inspect the history
   with `git log --oneline PREVIOUS_TAG..HEAD`, replacing `PREVIOUS_TAG`.

2. Run the checks and try packaging locally. Set `VERSION` to your chosen version;
   the example below is the first release.

   ```sh
   export VERSION=v0.1.0
   make check
   make interactions
   make package
   ./bin/mapbox-tui --version
   (cd dist && sha256sum --check "mapbox-tui-${VERSION}-linux-amd64.tar.gz.sha256")
   ```

   This creates the archive and checksum in `dist/`. It doesn't create a tag or
   upload anything. `make package` builds with CGO disabled and includes the
   binary, installation instructions, project licence and third-party notices.

3. Tag the checked commit and push just that tag:

   ```sh
   git tag -a "$VERSION" -m "Release $VERSION"
   git push origin "$VERSION"
   ```

   This starts **Release** in GitHub Actions. It reuses the CI workflow to run
   formatting, build, tests, vet, CLI/MCP parity, race and terminal checks against
   the tagged revision. Packaging then requires a valid version tag whose commit
   is on `main`. It builds and smoke-tests the archive before creating a draft.

4. Open the workflow run and wait for it to finish:

   ```sh
   gh run list --workflow release.yml --limit 5
   ```

   Open the matching run from its URL in GitHub, or use `gh run view RUN_ID --web`.

## Review and publish

1. Open the draft on the repository's [Releases page](https://github.com/adamcbrewer/mapbox-tui/releases).
   Check the version, commit and two attachments:
   `mapbox-tui-VERSION-linux-amd64.tar.gz` and its `.sha256` file.
2. Edit the generated notes into a short account of user-visible changes. Call
   out breaking changes, migration steps and known limitations. Link to the
   [installation instructions](INSTALL.md), and state that this release supports
   Linux x86-64. GitHub's generated notes include merged PRs; they don't infer a
   version or guarantee that breaking changes are explained.
3. Download both draft attachments into a fresh directory with
   `gh release download "$VERSION" --dir /path/to/empty-directory`. Verify and
   extract them using [Installation](INSTALL.md). Check `--version`, open a local
   track with `--no-provider`, and confirm the viewer starts and quits normally.
4. Click **Publish release** once you are satisfied. For a new release intended
   to be the default download, mark it as the latest release.

Keep published tags and assets unchanged. Users must be able to refer to a
specific version and checksum later.

## Failed runs and corrections

- If a run fails before creating a draft, fix any external/transient problem and
  rerun the failed jobs in Actions. If the source or workflow needs changing,
  merge the fix and use a new version tag.
- If a draft already exists, inspect it before retrying. `gh release create`
  deliberately refuses to overwrite it. To rebuild the same unchanged tag after
  an interrupted upload, delete only the unpublished draft in GitHub, leave its
  tag in place, then rerun the release job.
- If a published release has a defect, publish a fix under a new version. Users
  can reinstall an earlier binary using the same installation steps. Review any
  configuration migration before recommending a downgrade.
- To correct release prose, edit the release notes without replacing its assets.

## Files involved

- [Release workflow](../.github/workflows/release.yml): tag trigger, checks,
  packaging, smoke checks and draft creation.
- [CI workflow](../.github/workflows/ci.yml): shared pre-release and PR checks.
- [Makefile](../Makefile): build metadata, Linux archive and SHA-256 checksum.
- [PR-title workflow](../.github/workflows/pr-title.yml) and
  [Contributing](../CONTRIBUTING.md): contributor title format and validation.

Further reference: [GitHub release creation](https://cli.github.com/manual/gh_release_create)
and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
