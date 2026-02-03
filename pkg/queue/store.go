package queue

import "context"

type Store interface {
	CreateEntry(ctx context.Context, queueName, clientID string, entryTimestamp int64) error
	DeleteEntry(ctx context.Context, queueName string, entryTimestamp int64) error
	ListEntries(ctx context.Context, queueName string) ([]QueueEntry, error)
	UpdateHeartbeat(ctx context.Context, queueName string, entryTimestamp int64) error
	Close() error
}

type QueueEntry struct {
	QueueName      string
	ClientID       string
	EntryTimestamp int64
	LastUpdated    int64
}
