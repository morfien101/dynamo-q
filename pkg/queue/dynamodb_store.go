package queue

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

type DynamoDBStore struct {
	svc       *dynamodb.DynamoDB
	tableName string
}

func NewDynamoDBStore(sess *session.Session, tableName string) *DynamoDBStore {
	return &DynamoDBStore{svc: dynamodb.New(sess), tableName: tableName}
}

func (s *DynamoDBStore) CreateEntry(_ context.Context, queueName, clientID string, entryTimestamp int64) error {
	_, err := s.svc.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]*dynamodb.AttributeValue{
			"queueName": {
				S: aws.String(queueName),
			},
			"clientId": {
				S: aws.String(clientID),
			},
			"entryTimestamp": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
			"lastUpdated": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
		},
		// Never overwrite another client's entry. Matching our own clientId keeps retries idempotent.
		ConditionExpression: aws.String("attribute_not_exists(entryTimestamp) OR clientId = :clientId"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":clientId": {S: aws.String(clientID)},
		},
	})
	if isConditionalCheckFailed(err) {
		return ErrEntryExists
	}
	return err
}

func (s *DynamoDBStore) DeleteEntry(_ context.Context, queueName, clientID string, entryTimestamp int64) error {
	_, err := s.svc.DeleteItem(&dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]*dynamodb.AttributeValue{
			"queueName": {
				S: aws.String(queueName),
			},
			"entryTimestamp": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
		},
		ConditionExpression:       aws.String(clientIDCondition(clientID)),
		ExpressionAttributeValues: clientIDValues(clientID),
	})
	if isConditionalCheckFailed(err) {
		// Already gone or owned by someone else: nothing of ours to delete.
		return nil
	}
	return err
}

func (s *DynamoDBStore) ListEntries(_ context.Context, queueName string) ([]QueueEntry, error) {
	var entries []QueueEntry

	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("queueName = :queueName"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":queueName": {
				S: aws.String(queueName),
			},
		},
		ScanIndexForward: aws.Bool(true),
	}

	for {
		result, err := s.svc.Query(queryInput)
		if err != nil {
			return nil, err
		}

		for _, item := range result.Items {
			entry := QueueEntry{QueueName: queueName}

			if v, ok := item["queueName"]; ok && v.S != nil {
				entry.QueueName = *v.S
			}

			if v, ok := item["clientId"]; ok && v.S != nil {
				entry.ClientID = *v.S
			}

			if v, ok := item["entryTimestamp"]; ok && v.N != nil {
				if ts, err := strconv.ParseInt(*v.N, 10, 64); err == nil {
					entry.EntryTimestamp = ts
				}
			}

			if v, ok := item["lastUpdated"]; ok && v.N != nil {
				if lu, err := strconv.ParseInt(*v.N, 10, 64); err == nil {
					entry.LastUpdated = lu
				}
			}

			entries = append(entries, entry)
		}

		if result.LastEvaluatedKey == nil {
			break
		}

		queryInput.ExclusiveStartKey = result.LastEvaluatedKey
	}

	return entries, nil
}

func (s *DynamoDBStore) UpdateHeartbeat(_ context.Context, queueName, clientID string, entryTimestamp int64) error {
	_, err := s.svc.UpdateItem(&dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]*dynamodb.AttributeValue{
			"queueName": {
				S: aws.String(queueName),
			},
			"entryTimestamp": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
		},
		UpdateExpression: aws.String("SET lastUpdated = :lastUpdated"),
		// UpdateItem is an upsert; without this condition a heartbeat recreates a deleted entry with no clientId.
		ConditionExpression: aws.String("clientId = :clientId"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":lastUpdated": {
				N: aws.String(strconv.FormatInt(time.Now().Unix(), 10)),
			},
			":clientId": {S: aws.String(clientID)},
		},
	})
	if isConditionalCheckFailed(err) {
		return ErrEntryNotFound
	}
	return err
}

// clientIDCondition matches entries owned by clientID. An empty clientID targets
// malformed entries that have no clientId attribute at all.
func clientIDCondition(clientID string) string {
	if clientID == "" {
		return "attribute_exists(entryTimestamp) AND attribute_not_exists(clientId)"
	}
	return "clientId = :clientId"
}

func clientIDValues(clientID string) map[string]*dynamodb.AttributeValue {
	if clientID == "" {
		return nil
	}
	return map[string]*dynamodb.AttributeValue{":clientId": {S: aws.String(clientID)}}
}

func isConditionalCheckFailed(err error) bool {
	var aerr awserr.Error
	return errors.As(err, &aerr) && aerr.Code() == dynamodb.ErrCodeConditionalCheckFailedException
}

func (s *DynamoDBStore) Close() error {
	return nil
}
