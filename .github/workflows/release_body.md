# Waiter waits for the queue manager to start

## Fixes
* **`client-wait` could fail with `connection refused`.** `server-start` runs the manager in the background
  and returns straight away. If `client-wait` ran before the manager was listening, the waiter failed at once:
  ```
  Could not check queue status: could not subscribe to queue status: rpc error: code = Unavailable
  desc = connection error: ... dial tcp [::1]:50051: connect: connection refused
  ```
  The waiter now waits up to 30 seconds for the manager to accept connections before it sends any request.
  This applies to `-wait-for-turn`, `-try-once` and `-shutdown`.

## Upgrade notes
* Only the waiter binary changed. No action, table or collection changes are needed.
* Jobs that use the default `version: latest` get the fix once this release is out. Jobs that pin `version`
  need to move to this release.
