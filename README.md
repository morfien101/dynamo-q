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

# AWS Configuration required

## DynamoDB Table

I am going to put the terraform block that creates the table below as it gives a really easy to read expression of the table that can be converted with ease to whatever you use.

```tf
resource "aws_dynamodb_table" "github_actions_dynamoq" {
  name           = "github-actions-dynamo-q-queue"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "queueName"
  range_key      = "entryTimestamp"
  table_class    = "STANDARD"

  attribute {
    name = "queueName"
    type = "S"
  }
  attribute {
    name = "entryTimestamp"
    type = "N"
  }
  ttl {
    attribute_name = "lastUpdated"
    enabled        = true
  }
  tags = {
    Name = "Github Actions Queue Table"
  }
}
```

## IAM Policy

Remember that you will need to grant the github job access to AWS to deal with the dynamoDB table. This can be done in many
different ways. However I would recommend to make use of the OIDC token in github already. Below is the minimum required
permissions to get queuing working.

**Policy**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": [
        "dynamodb:UpdateItem",
        "dynamodb:Query",
        "dynamodb:PutItem",
        "dynamodb:GetItem",
        "dynamodb:DeleteItem",
        "dynamodb:ConditionCheckItem"
      ],
      "Effect": "Allow",
      "Resource": [
        "arn:aws:dynamodb:ap-south-1:<account number>:table/github-actions-dynamo-q-queue"
      ],
      "Sid": "DynamoQPermissionsSet"
    }
  ]
}
```

**Trust Relationship for OIDC**
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::<account number>:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
            "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:<org name>/*"
        }
      }
    }
  ]
}
```

# Usage

Below is an example job you can use to setup the queue.

```yaml
name: Test DynamoQ

on:
  workflow_dispatch:

permissions:
  id-token: write

jobs:
  do_it:
    runs-on: ubuntu-latest
    steps:
      # Setup Auth for connecting to DynamoDB
      - name: AWS Auth
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789:role/github-actions-dynamoq-testing
          aws-region: ap-south-1
      # Starts the server in the background
      - name: setup lock
        uses: morfien101/dynamo-q@<current_version>
        env:
          AWS_REGION: ap-south-1
        with:
          action: server-start
          queue-name: randy-tester-github-action
          queue-table: QueueTable
      # Waits for the action to get to the front of the queue
      - name: wait for lock
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: client-wait

      # This is where you can start to do your work
      - name: do something
        run: echo "I am doing something"
      
      # Remember to leave the queue.
      # Don't worry if your job crashes, your queue slow will expired after 2 mins
      # and the next in line will remove your zombie queue member and carry on.
      - name: release lock
        if: always()
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: server-stop
```

# Generate gRPC code

Install grpc code generator on mac. 
These tools are available on just about all platforms. I just happen to have a mac.

```sh
brew install protoc-gen-go-grpc
brew install protoc-gen-go
```

```sh
cd pkg/comms
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  queueservice.proto
```

The rest of the code is just simple Go.

# Contributing

I am happy to look at PRs should you want to contribute.