# Binaries match the action version

## Fixes
* **The download step could fail on GitHub.** With the default `version: latest`, the action asked the
  GitHub REST API for the latest release. That API is rate limited per runner IP, so the call sometimes
  failed and the job failed with it. The action no longer calls the API.
* **A failed download now fails the step.** Before, a 404 or rate-limit response was saved and run as the
  binary. The download now retries 5 times, then fails with a clear error.

## Changes
* **The binaries now match the action ref.** If you don't set `version`, the action downloads the release
  that matches the ref you use:
  * `morfien101/dynamo-q@v2` downloads the `v2` release, which moves with the `v2` tag.
  * `morfien101/dynamo-q@v2.2.0` downloads the `v2.2.0` release.
  * A branch or commit SHA downloads the latest release.
* Binaries are now saved in `$RUNNER_TEMP`, not `/tmp`. `$RUNNER_TEMP` is emptied for each job, so a
  self-hosted runner can't reuse an old binary from an earlier job.

## Upgrade notes
* No table, collection or input changes are needed.
* The default for `version` changed from `latest` to empty, which means "match the action ref". To keep the
  old behaviour, set `version: latest`. A pinned `version` works as before.
