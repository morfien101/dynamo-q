package queue

import (
	"context"
	"math/rand"
	"time"

	log "github.com/sirupsen/logrus"
)

func IsQueueMemberZombie(lastUpdated int64) bool {
	threshold := int64(120) // 2 minutes in seconds
	return time.Now().Unix()-lastUpdated > threshold
}

func StartHeartbeat(ctx context.Context, store Store, queueName string, entryTimestamp int64, stop <-chan bool) {
	go func() {
		waitQ := make(chan bool)
		wait := func(c chan bool) {
			waitTime := rand.Int63n(15) + 10
			time.Sleep(time.Duration(waitTime) * time.Second)
			c <- true
		}
		for {
			go wait(waitQ)

			select {
			case <-ctx.Done():
				return
			case <-waitQ:
				log.WithFields(log.Fields{"queueName": queueName}).Debug("Update lastUpdated")
				if err := store.UpdateHeartbeat(ctx, queueName, entryTimestamp); err != nil {
					log.WithFields(log.Fields{"queueName": queueName, "error": err}).Error("Error updating lastUpdated")
				}
			case <-stop:
				log.WithFields(log.Fields{"queueName": queueName}).Debug("Stopping heartbeat")
				return
			}
		}
	}()
}

func WaitForTurn(ctx context.Context, store Store, queueName, clientID string) chan bool {
	signalPipe := make(chan bool)
	go func() {
		for {
			log.Info("Checking queue...")
			atFront, err := isClientAtFrontOfQueue(ctx, store, queueName, clientID)
			if err != nil {
				log.Error("Error checking queue:", err)
				signalPipe <- false
				return
			}

			if atFront {
				signalPipe <- true
				return
			}
			waitTime := int64(rand.Intn(15) + 15)
			log.Infof("Not at front of queue. Waiting %d seconds till next check...", waitTime)
			time.Sleep(time.Duration(waitTime) * time.Second)
		}
	}()
	return signalPipe
}

func getQueueEntries(ctx context.Context, store Store, queueName string) ([]QueueEntry, error) {
	return store.ListEntries(ctx, queueName)
}

func checkFrontOfQueue(ctx context.Context, store Store, queueName, clientID string) (bool, []QueueEntry, error) {
	entries, err := getQueueEntries(ctx, store, queueName)
	if err != nil {
		return false, []QueueEntry{}, err
	}

	atFront := false
	zombies := []QueueEntry{}

	for _, entry := range entries {
		if IsQueueMemberZombie(entry.LastUpdated) {
			zombies = append(zombies, entry)
			continue
		}

		if entry.ClientID == clientID {
			atFront = true
		}

		break
	}

	return atFront, zombies, nil
}

func deleteZombies(ctx context.Context, store Store, queueName string, zombies []QueueEntry) error {
	for _, zombie := range zombies {
		log.WithFields(log.Fields{
			"clientId":       zombie.ClientID,
			"entryTimestamp": zombie.EntryTimestamp,
		}).Warn("Deleting zombie queue entry for client")

		if err := store.DeleteEntry(ctx, queueName, zombie.EntryTimestamp); err != nil {
			return err
		}
	}

	return nil
}

func isClientAtFrontOfQueue(ctx context.Context, store Store, queueName, clientID string) (bool, error) {
	atFront, zombies, err := checkFrontOfQueue(ctx, store, queueName, clientID)
	if err != nil {
		return false, err
	}

	if len(zombies) > 0 {
		if err := deleteZombies(ctx, store, queueName, zombies); err != nil {
			return false, err
		}
	}

	return atFront, nil
}
