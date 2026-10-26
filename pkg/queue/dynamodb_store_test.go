package queue

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

// Replays the deadlock from issue #2. Needs DynamoDB Local:
//
//	docker run --rm -p 8000:8000 amazon/dynamodb-local
//	DYNAMODB_ENDPOINT=http://localhost:8000 go test ./pkg/queue/
func TestDynamoDBStoreSameSecondCollision(t *testing.T) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set")
	}
	ctx := context.Background()
	sess := session.Must(session.NewSession(&aws.Config{
		Endpoint:    aws.String(endpoint),
		Region:      aws.String("us-east-1"),
		Credentials: credentials.NewStaticCredentials("x", "x", ""),
	}))
	table := "dynamoq-test"
	svc := dynamodb.New(sess)
	_, err := svc.CreateTable(&dynamodb.CreateTableInput{
		TableName: aws.String(table),
		AttributeDefinitions: []*dynamodb.AttributeDefinition{
			{AttributeName: aws.String("queueName"), AttributeType: aws.String("S")},
			{AttributeName: aws.String("entryTimestamp"), AttributeType: aws.String("N")},
		},
		KeySchema: []*dynamodb.KeySchemaElement{
			{AttributeName: aws.String("queueName"), KeyType: aws.String("HASH")},
			{AttributeName: aws.String("entryTimestamp"), KeyType: aws.String("RANGE")},
		},
		BillingMode: aws.String("PAY_PER_REQUEST"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.DeleteTable(&dynamodb.DeleteTableInput{TableName: aws.String(table)})

	s := NewDynamoDBStore(sess, table)
	q, ts := "q", time.Now().Unix()

	if err := s.CreateEntry(ctx, q, "A", ts); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEntry(ctx, q, "A", ts); err != nil {
		t.Fatalf("retrying own create should succeed: %v", err)
	}
	if err := s.CreateEntry(ctx, q, "B", ts); !errors.Is(err, ErrEntryExists) {
		t.Fatalf("B must not overwrite A, got %v", err)
	}
	if err := s.CreateEntry(ctx, q, "B", ts+1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHeartbeat(ctx, q, "B", ts); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("B must not heartbeat A's entry, got %v", err)
	}
	if err := s.DeleteEntry(ctx, q, "B", ts); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHeartbeat(ctx, q, "A", ts); err != nil {
		t.Fatalf("A's entry must survive B's delete: %v", err)
	}

	// Heartbeat after delete must not recreate the item.
	if err := s.DeleteEntry(ctx, q, "A", ts); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHeartbeat(ctx, q, "A", ts); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("want ErrEntryNotFound, got %v", err)
	}

	// A clientId-less item (left by older versions) is cleared instead of blocking the queue.
	_, err = svc.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item: map[string]*dynamodb.AttributeValue{
			"queueName":      {S: aws.String(q)},
			"entryTimestamp": {N: aws.String(strconv.FormatInt(ts-50, 10))},
			"lastUpdated":    {N: aws.String(strconv.FormatInt(ts, 10))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isClientAtFrontOfQueue(ctx, s, q, "B"); err != nil {
		t.Fatal(err)
	}
	if atFront, err := isClientAtFrontOfQueue(ctx, s, q, "B"); err != nil || !atFront {
		t.Fatalf("B should be at the front once the malformed entry is removed: %v %v", atFront, err)
	}
}
