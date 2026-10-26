package queue

import (
	"context"
	"errors"
)

var (
	// ErrEntryExists is returned by CreateEntry when another client already owns the key.
	ErrEntryExists = errors.New("queue entry already exists for another client")
	// ErrEntryNotFound is returned by UpdateHeartbeat when the client's entry no longer exists.
	ErrEntryNotFound = errors.New("queue entry not found for client")
)

// Store implementations must scope writes to clientID:
// CreateEntry never overwrites another client's entry, UpdateHeartbeat never creates an entry,
// and DeleteEntry only removes an entry owned by clientID (a no-op otherwise).
type Store interface {
	CreateEntry(ctx context.Context, queueName, clientID string, entryTimestamp int64) error
	DeleteEntry(ctx context.Context, queueName, clientID string, entryTimestamp int64) error
	ListEntries(ctx context.Context, queueName string) ([]QueueEntry, error)
	UpdateHeartbeat(ctx context.Context, queueName, clientID string, entryTimestamp int64) error
	Close() error
}

type QueueEntry struct {
	QueueName      string
	ClientID       string
	EntryTimestamp int64
	LastUpdated    int64
}
