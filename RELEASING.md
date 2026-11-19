# Releases

Releases use [Release Please](https://github.com/googleapis/release-please) on the GitHub default branch. The first stable version is `1.0.0`; subsequent versions follow Semantic Versioning and Conventional Commits:

- `fix:` creates a patch release;
- `feat:` creates a minor release;
- a commit with `!` or a `BREAKING CHANGE:` footer creates a major release.

Release Please opens or updates a release pull request with the version and changelog. Merging that pull request creates a `vX.Y.Z` Git tag and a published GitHub Release. Releases are stable (not drafts or prereleases). The workflow then builds the tagged Docker image and publishes `ghcr.io/joselarantes/tucano-indices:X.Y.Z` and `ghcr.io/joselarantes/tucano-indices:latest`.

The deployment workflow also builds and publishes `latest` plus a full commit SHA tag when a regular pull request is merged into `main`. It skips Release Please branches. When Release Please creates a versioned release, its workflow calls the deployment workflow with the generated version and Git tag, so the image receives the matching version tag and `latest`. Deployment builds the Docker image and does not repeat CI tests.

The workflow uses `GITHUB_TOKEN` with contents, issues, and pull request write permissions. In the repository settings, enable **Allow GitHub Actions to create and approve pull requests** under **Settings → Actions → General** so the workflow can open its release pull request.
