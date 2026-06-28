# Releasing amq-noc

Release changes must go through a release branch, PR review, and `make ci`
before merge. Do not tag from an unmerged local worktree; tag only the merge
commit on `main`.

`amq-noc` follows the same `go install`-based distribution as the rest of the
toolchain: the published version is whatever `amq-noc version` reports from the
binary built at a tag, so every release must verify that the documented install
path round-trips the tag.

## Release Checklist

1. Start from an up-to-date `main`, then create a release branch named for the
   target tag, for example:

   ```sh
   git fetch --all --prune --tags
   git switch main
   git pull --ff-only
   git switch -c feat/v0.11.0
   ```

2. Confirm the public command surface is still narrow: `amq-noc`, `amq-noc noc`,
   and `amq-noc version [--json]`. Copied non-NOC lifecycle commands must stay
   unreachable.
3. Update user-facing install references, usually the README `go install` tag,
   and any release notes or companion-version docs.
4. Run the local gates:

   ```sh
   gofmt -l .
   git diff --check
   go test ./...
   go vet ./...
   make ci
   ```

5. Commit the release candidate, push the release branch, and open a PR against
   `main`.
6. Review the PR before merging or tagging:

   ```sh
   gh pr diff <number> --name-only
   gh pr diff <number> --patch
   gh pr view <number> --json state,mergeStateStatus,reviewDecision,statusCheckRollup
   ```

   Confirm the PR is the intended release diff, CI is green, required review is
   satisfied, and no unrelated work is included. If the review finds release
   risk or stale docs, update the branch and repeat the local gates.
7. Merge the release PR, then fetch `main` and verify `HEAD` is the merge
   commit.
8. Tag the merge commit:

   ```sh
   git switch main
   git pull --ff-only
   git tag -a v0.11.0 -m "amq-noc v0.11.0"
   git push origin v0.11.0
   ```

9. Create the GitHub release for the tag.
10. Smoke test the published install path:

   ```sh
   make release-smoke VERSION=v0.11.0
   ```

   The smoke test installs `github.com/omriariav/amq-noc/cmd/amq-noc@VERSION`
   into a throwaway `GOBIN` and fails unless `amq-noc version` prints the same
   version. This catches releases where the source tag works but the documented
   `go install` path reports `dev` or an old version.

## Patch Releases

Follow the same checklist with the new patch tag (for example `v0.2.1`). Keep the
README install tag and the `make release-smoke VERSION=...` argument in lockstep
with the tag you push.

## Notes

- `amq-noc` is the NOC and dispatcher surface. Confirmed mutating actions remain
  preview-first and confirm-gated, and squad lifecycle/config actions are
  delegated to the installed `amq-squad` CLI; releasing `amq-noc` does not
  release `amq-squad`.
- A major version bump (1.x and beyond) is out of scope for the 0.x line. When
  it happens it requires the Go `/vN` module-path migration before the tag is
  `go install`-able; document that path here at that time.
