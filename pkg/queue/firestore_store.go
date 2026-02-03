package queue

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

type FirestoreStore struct {
	client     *firestore.Client
	collection string
}

func NewFirestoreStore(ctx context.Context, projectID, databaseID, collection string) (*FirestoreStore, error) {
	var (
		client *firestore.Client
		err    error
	)
	if databaseID != "" {
		client, err = firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	} else {
		client, err = firestore.NewClient(ctx, projectID)
	}
	if err != nil {
		return nil, err
	}
	return &FirestoreStore{client: client, collection: collection}, nil
}

func (s *FirestoreStore) CreateEntry(ctx context.Context, queueName, clientID string, entryTimestamp int64) error {
	docID := firestoreDocID(queueName, entryTimestamp)
	_, err := s.client.Collection(s.collection).Doc(docID).Set(ctx, map[string]interface{}{
		"queueName":      queueName,
		"clientId":       clientID,
		"entryTimestamp": entryTimestamp,
		"lastUpdated":    entryTimestamp,
	})
	return err
}

func (s *FirestoreStore) DeleteEntry(ctx context.Context, queueName string, entryTimestamp int64) error {
	docID := firestoreDocID(queueName, entryTimestamp)
	_, err := s.client.Collection(s.collection).Doc(docID).Delete(ctx)
	return err
}

func (s *FirestoreStore) ListEntries(ctx context.Context, queueName string) ([]QueueEntry, error) {
	entries := []QueueEntry{}
	iter := s.client.Collection(s.collection).
		Where("queueName", "==", queueName).
		OrderBy("entryTimestamp", firestore.Asc).
		Documents(ctx)
	defer iter.Stop()

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		data := doc.Data()
		entry := QueueEntry{QueueName: queueName}

		if v, ok := data["queueName"].(string); ok {
			entry.QueueName = v
		}
		if v, ok := data["clientId"].(string); ok {
			entry.ClientID = v
		}
		if v, ok := data["entryTimestamp"]; ok {
			entry.EntryTimestamp = toInt64(v)
		}
		if v, ok := data["lastUpdated"]; ok {
			entry.LastUpdated = toInt64(v)
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

func (s *FirestoreStore) UpdateHeartbeat(ctx context.Context, queueName string, entryTimestamp int64) error {
	docID := firestoreDocID(queueName, entryTimestamp)
	_, err := s.client.Collection(s.collection).Doc(docID).Update(ctx, []firestore.Update{{
		Path:  "lastUpdated",
		Value: time.Now().Unix(),
	}})
	return err
}

func (s *FirestoreStore) Close() error {
	return s.client.Close()
}

func firestoreDocID(queueName string, entryTimestamp int64) string {
	safeQueue := base64.RawURLEncoding.EncodeToString([]byte(queueName))
	return fmt.Sprintf("%s-%d", safeQueue, entryTimestamp)
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	default:
		return 0
	}
}
