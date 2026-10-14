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

Run these commands from a clean checkout on Linux x86-64. Use the Go version in
`go.mod`, Make, Python 3.11 or newer, and the terminal-check dependencies listed
in [Development](DEVELOPMENT.md). Install and authenticate GitHub CLI for `gh`.
You need permission to push tags and manage releases.

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
