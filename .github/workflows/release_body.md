# Adding in GCP support

This release adds in GCP (Google) support by making use of FireStore.
It should still be pretty much free since usage will be low unless you have lots of jobs.

See the updated README for configuration instructions.

## Additional updates
* Apple Silicon and MacOS no longer support UPX. In an effort to keep the job as quick as possible
  the binaries are compressed with UPX, however since Apple have done something that breaks this
  we can no longer use if for darwin based go binaries. This means that darwin binaries will just be
  bigger by about 50%.
* The action now has addition inputs for logging that will set the log level for the server.
  Useful if you are debugging access issues.
