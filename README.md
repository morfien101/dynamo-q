# Dynamo-Q
Queue process for AWS DynamoDB or Google Cloud Firestore.

Primarily intended to be used with Github actions to allow jobs to queue up.
Be warned, queuing jobs consume usage minutes as they are queued while running.

Github actions only have a queue of running + 1 at most.
The concurrency model will cancel any queued job that is trying to get a hold on a lock.
This sucks when you want all the jobs to run.

This action solves this.

You will need access to DynamoDB (AWS) or Firestore (GCP) for this action to work.
Instruction for setup below.

# AWS Configuration required

## DynamoDB Table

This Terraform block that creates the table below, gives a really easy to read expression of the table that can be converted with ease to whatever you use.

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

## Terraform (AWS IAM, optional)

If you manage IAM with Terraform, this is a minimal example for OIDC + role + policy:

```tf
resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["<github_oidc_thumbprint>"]
}

resource "aws_iam_role" "dynamoq" {
  name = "github-actions-dynamoq"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = aws_iam_openid_connect_provider.github.arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        }
        StringLike = {
          "token.actions.githubusercontent.com:sub" = "repo:<org>/<repo>:*"
        }
      }
    }]
  })
}

resource "aws_iam_policy" "dynamoq" {
  name = "dynamoq-policy"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "dynamodb:UpdateItem",
        "dynamodb:Query",
        "dynamodb:PutItem",
        "dynamodb:GetItem",
        "dynamodb:DeleteItem",
        "dynamodb:ConditionCheckItem"
      ]
      Resource = [
        "arn:aws:dynamodb:ap-south-1:<account number>:table/github-actions-dynamo-q-queue"
      ]
    }]
  })
}

resource "aws_iam_role_policy_attachment" "dynamoq" {
  role       = aws_iam_role.dynamoq.name
  policy_arn = aws_iam_policy.dynamoq.arn
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

# Usage for AWS

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
          backend: aws
      # Waits for the action to get to the front of the queue
      - name: wait for lock
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: client-wait

      # This is where you can start to do your work
      - name: do something
        run: echo "I am doing something"
      
      # Remember to leave the queue.
      # Don't worry if your job crashes, your queue entry will expired after 2 mins
      # and the next in line will remove your zombie queue member and carry on.
      - name: release lock
        if: always()
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: server-stop


# GCP Firestore Configuration

## Firestore collection

Firestore uses a single collection for queue entries. Reuse the same `queue-table` input as the collection name.

Each document stores:

- `queueName` (string)
- `clientId` (string)
- `entryTimestamp` (number)
- `lastUpdated` (number)

The action queries by `queueName` and orders by `entryTimestamp`. Firestore will require a composite index for
`queueName` + `entryTimestamp`, which is included in the terraform below.

## IAM Permissions (Workload Identity Federation)

Use GitHub OIDC to impersonate a Google service account. The service account needs Firestore access:

- `roles/datastore.user` (read/write)

Grant the GitHub principal access to impersonate the service account:

- `roles/iam.workloadIdentityUser` on the service account, to the GitHub principal set.

## Terraform (GCP)

This is a minimal example to create the Firestore database, service account, and workload identity wiring:

```tf
resource "google_project_service" "firestore" {
  project = var.project_id
  service = "firestore.googleapis.com"
}

resource "google_firestore_database" "default" {
  project     = var.project_id
  name        = "github-dynamoq"
  location_id = "us-central"
  type        = "FIRESTORE_NATIVE"
}

resource "google_service_account" "dynamoq" {
  project      = var.project_id
  account_id   = "github-actions-dynamoq"
  display_name = "GitHub Actions DynamoQ"
}

resource "google_project_iam_member" "firestore_user" {
  project = var.project_id
  role    = "roles/firestore.user"
  member  = "serviceAccount:${google_service_account.dynamoq.email}"
}

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github-actions"
  display_name              = "GitHub Actions"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub OIDC"
  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
  attribute_mapping = {
    "google.subject" = "assertion.sub"
  }
}

resource "google_service_account_iam_member" "github_wif" {
  service_account_id = google_service_account.dynamoq.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/<org>/<repo>"
}

# This sets up the index required for the queues when you use a collection called github-queues
# Something like this on the cli: -queue-table github-queues -firestore-database github-dynamoq
# Change names as required for the database and table.
resource "google_firestore_index" "github_queues_queueName_entryTimestamp" {
  project    = var.project_id
  database   = "github-dynamoq"
  collection = "github-queues"

  fields {
    field_path = "queueName"
    order      = "ASCENDING"
  }

  fields {
    field_path = "entryTimestamp"
    order      = "ASCENDING"
  }

  fields {
    field_path = "__name__"
    order      = "ASCENDING"
  }
}
```

## Example workflow (GCP)

```yaml
name: Test DynamoQ (GCP)

on:
  workflow_dispatch:

permissions:
  id-token: write

jobs:
  do_it:
    runs-on: ubuntu-latest
    steps:
      - name: GCP Auth
        uses: google-github-actions/auth@v2
        with:
          workload_identity_provider: projects/123456789/locations/global/workloadIdentityPools/pool-id/providers/provider-id
          service_account: github-actions-queue@your-project.iam.gserviceaccount.com

      - name: setup lock
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: server-start
          queue-name: randy-tester-github-action
          queue-table: QueueCollection
          backend: gcp
          gcp-project: your-project-id

      - name: wait for lock
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: client-wait

      - name: do something
        run: echo "I am doing something"

      - name: release lock
        if: always()
        uses: morfien101/dynamo-q@<current_version>
        with:
          action: server-stop
```

The `backend` input defaults to `aws`. When using `gcp`, set `gcp-project` or the `GCP_PROJECT`/`GOOGLE_CLOUD_PROJECT` environment variables.
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
