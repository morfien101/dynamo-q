# Queue deadlock fix and visible manager logs

## Fixes
* **Queue could deadlock when two jobs joined in the same second** ([#2](https://github.com/morfien101/dynamo-q/issues/2)).
  The second job overwrote the first job's queue entry. Once that entry was released, the first job's
  heartbeat recreated it with no `clientId`, and the queue was blocked until someone deleted it by hand.
  * Joining the queue never overwrites another job's entry. A job that collides moves to the next second instead.
  * Heartbeats never create an entry. If a job's entry disappears before it reaches the front, the manager
    exits and `client-wait` fails instead of waiting forever.
  * A job can only delete its own entry.
  * Entries with no `clientId` (for example, ones left by older versions) are treated as zombies and removed.
  * The same rules apply to both the DynamoDB and the Firestore backends. No table or collection changes are needed.

## Improvements
* **The manager's log now shows in the job output.** `server-start` writes it to a file in `RUNNER_TEMP`.
  `client-wait` prints it live while waiting, and `server-stop` prints the rest in a collapsed group.
  Heartbeat errors and zombie removals are no longer hidden.

## Upgrade notes
* Use the same version for the `server-start`, `client-wait` and `server-stop` steps.
* The binaries are downloaded from the `latest` release by default, so the fixes reach every job once this
  release is out. Getting the log output needs the action ref that includes this release.
