package tables

import (
	"math/rand"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	log "github.com/sirupsen/logrus"
)

func CreateQueueEntry(svc *dynamodb.DynamoDB, queueName, clientId string, entryTimestamp int64, tableName string, stopHeartBeat chan bool) error {
	_, err := svc.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item: map[string]*dynamodb.AttributeValue{
			"queueName": {
				S: aws.String(queueName),
			},
			"clientId": {
				S: aws.String(clientId),
			},
			"entryTimestamp": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
			"lastUpdated": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
		},
	})

	// start heartbeat process
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
			case <-waitQ:
				log.WithFields(log.Fields{"clientId": clientId}).Debug("Update lastUpdated")
				err := updateQueueLastUpdated(svc, queueName, entryTimestamp, tableName)
				if err != nil {
					log.WithFields(log.Fields{"clientId": clientId, "tableName": tableName, "error": err}).Error("Error updating lastUpdated")
				}
			case <-stopHeartBeat:
				log.WithFields(log.Fields{"clientId": clientId}).Debug("Stoping heartbeat")
				return
			}
		}
	}()

	return err
}

func DeleteQueueEntry(svc *dynamodb.DynamoDB, queueName, clientId string, entryTimestamp int64, tableName string) error {
	_, err := svc.DeleteItem(&dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]*dynamodb.AttributeValue{
			"queueName": {
				S: aws.String(queueName),
			},
			"entryTimestamp": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
		},
	})
	return err
}

type QueueEntry struct {
	queueName      string
	ClientId       string
	EntryTimestamp int64
	LastUpdated    int64
	Zombie         bool
}

func getQueueEntries(svc *dynamodb.DynamoDB, queueName, tableName string) ([]QueueEntry, error) {
	var entries []QueueEntry

	// Define the initial query input
	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		KeyConditionExpression: aws.String("queueName = :queueName"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":queueName": {
				S: aws.String(queueName),
			},
		},
		ScanIndexForward: aws.Bool(true), // true for ascending, false for descending
	}

	for {
		// Execute the query
		result, err := svc.Query(queryInput)
		if err != nil {
			return nil, err
		}

		// Parse the result items into QueueEntry structs
		for _, item := range result.Items {
			entry := QueueEntry{}

			if v, ok := item["queueName"]; ok && v.S != nil {
				entry.queueName = *v.S
			}

			if v, ok := item["clientId"]; ok && v.S != nil {
				entry.ClientId = *v.S
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

		// Check for LastEvaluatedKey to determine if there are more items to fetch
		if result.LastEvaluatedKey == nil {
			break
		}

		// Set the ExclusiveStartKey for the next page
		queryInput.ExclusiveStartKey = result.LastEvaluatedKey
	}

	return entries, nil
}

func IsQueueMemberZombie(lastUpdated int64) bool {
	threshold := 120 // 2 minutes in seconds
	return time.Now().Unix()-lastUpdated > int64(threshold)
}

func checkFrontOfQueue(svc *dynamodb.DynamoDB, queueName, clientId, tableName string) (bool, []QueueEntry, error) {
	entries, err := getQueueEntries(svc, queueName, tableName)
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

		// If the current entry is not a zombie, check if it's the current client
		if entry.ClientId == clientId {
			atFront = true
		}

		break
	}

	return atFront, zombies, nil // The queue is empty or only contains the current client
}

func deleteZombies(svc *dynamodb.DynamoDB, queueName, tableName string, zombies []QueueEntry) error {
	for _, zombie := range zombies {
		log.WithFields(log.Fields{
			"clientId":       zombie.ClientId,
			"entryTimestamp": zombie.EntryTimestamp,
		}).Warn("Deleting zombie queue entry for client")

		err := DeleteQueueEntry(svc, queueName, zombie.ClientId, zombie.EntryTimestamp, tableName)
		if err != nil {
			return err
		}
	}

	return nil
}

func isClientAtFrontOfQueue(svc *dynamodb.DynamoDB, queueName, clientId, tableName string) (bool, error) {
	atFront, zombies, err := checkFrontOfQueue(svc, queueName, clientId, tableName)
	if err != nil {
		return false, err
	}

	if len(zombies) > 0 {
		err = deleteZombies(svc, queueName, tableName, zombies)
		if err != nil {
			return false, err
		}
	}

	return atFront, nil
}

func WaitForTurn(svc *dynamodb.DynamoDB, queueName, clientId, tableName string) chan bool {
	signalPipe := make(chan bool)
	go func() {
		for {
			log.Info("Checking queue...")
			atFront, err := isClientAtFrontOfQueue(svc, queueName, clientId, tableName)
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

func updateQueueLastUpdated(svc *dynamodb.DynamoDB, queueName string, entryTimestamp int64, tableName string) error {
	_, err := svc.UpdateItem(&dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]*dynamodb.AttributeValue{
			"queueName": {
				S: aws.String(queueName),
			},
			"entryTimestamp": {
				N: aws.String(strconv.FormatInt(entryTimestamp, 10)),
			},
		},
		UpdateExpression: aws.String("SET lastUpdated = :lastUpdated"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":lastUpdated": {
				N: aws.String(strconv.FormatInt(time.Now().Unix(), 10)),
			},
		},
	})
	return err
}
