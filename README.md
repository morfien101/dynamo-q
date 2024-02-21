# Dynamo-Q
Queue process for AWS DynamoDB.

Primarily intended to be used with Github actions to allow jobs to queue up.
Be warned, queuing jobs consume usage minutes.

Github actions only have a queue of running + 1 at most.
The concurrency model will cancel any queued job that is trying to get a hold on a lock.
This sucks when you want all the jobs to run.

This action solves this.

You will need to have access to AWS DynamoDB for this action to work.
Instruction for setup below.

Coming soon...

# Generate gRPC code

Install generator on mac

```sh
brew install protoc-gen-go-grpc
brew install protoc-gen-go
```

```sh
cd pkg/comms
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       lockservice.proto
```

# Contributing

I am happy to look at PRs should you want to contribute.