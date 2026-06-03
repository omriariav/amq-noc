# Releasing amq-noc

Release changes must go through a PR and `make ci` before merge.

`amq-noc` follows the same `go install`-based distribution as the rest of the
toolchain: the published version is whatever `amq-noc version` reports from the
binary built at a tag, so every release must verify that the documented install
path round-trips the tag.

## 0.1.0 Release Checklist

1. Confirm the public command surface is still narrow: `amq-noc`, `amq-noc noc`,
   and `amq-noc version [--json]`. Copied non-NOC lifecycle commands must stay
   unreachable.
2. Update user-facing install references, usually the README `go install` tag.
3. Run the local gates:

   ```sh
   gofmt -l .
   git diff --check
   go test ./...
   make ci
   ```

4. Merge the release PR.
5. Tag the merge commit:

   ```sh
   git tag -a v0.1.0 -m "amq-noc v0.1.0"
   git push origin v0.1.0
   ```

6. Create the GitHub release for the tag.
7. Smoke test the published install path:

   ```sh
   make release-smoke VERSION=v0.1.0
   ```

   The smoke test installs `github.com/omriariav/amq-noc/cmd/amq-noc@VERSION`
   into a throwaway `GOBIN` and fails unless `amq-noc version` prints the same
   version. This catches releases where the source tag works but the documented
   `go install` path reports `dev` or an old version.

## Patch Releases

Follow the same checklist with the new patch tag (for example `v0.1.1`). Keep the
README install tag and the `make release-smoke VERSION=...` argument in lockstep
with the tag you push.

## Notes

- `amq-noc` is the NOC and dispatcher surface. Confirmed mutating actions remain
  preview-first and confirm-gated, and squad lifecycle/config actions are
  delegated to the installed `amq-squad` CLI; releasing `amq-noc` does not
  release `amq-squad`.
- A major version bump (1.x and beyond) is out of scope for the 0.1.x line. When
  it happens it requires the Go `/vN` module-path migration before the tag is
  `go install`-able; document that path here at that time.
