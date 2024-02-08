package tables

import (
	"math/rand"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	log "github.com/sirupsen/logrus"
)

func AcquireLock(svc *dynamodb.DynamoDB, lockId, ownerId, tableName string, stopHeartBeat chan bool) error {
	leaseExpiration := time.Now().Add(30 * time.Second).Unix() // 30 seconds lease

	_, err := svc.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item: map[string]*dynamodb.AttributeValue{
			"lockId": {
				S: aws.String(lockId),
			},
			"ownerId": {
				S: aws.String(ownerId),
			},
			"leaseExpiration": {
				N: aws.String(strconv.FormatInt(leaseExpiration, 10)),
			},
		},
		// Ensure the lock doesn't already exist
		ConditionExpression: aws.String("attribute_not_exists(lockId)"),
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
				log.WithFields(log.Fields{"lockId": lockId}).Debug("Update lock lastUpdated")
				err := updateLockLastUpdated(svc, lockId, tableName)
				if err != nil {
					log.WithFields(log.Fields{"lockId": lockId, "tableName": tableName, "error": err}).Error("Error updating lock lastUpdated")
				}
			case <-stopHeartBeat:
				log.WithFields(log.Fields{"lockId": lockId}).Debug("Stopping lock heartbeat")
				return
			}
		}
	}()

	return err
}

func ReleaseLock(svc *dynamodb.DynamoDB, lockId, tableName string) error {
	_, err := svc.DeleteItem(&dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]*dynamodb.AttributeValue{
			"lockId": {
				S: aws.String(lockId),
			},
		},
	})
	return err
}

func updateLockLastUpdated(svc *dynamodb.DynamoDB, lockId, tableName string) error {
	_, err := svc.UpdateItem(&dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]*dynamodb.AttributeValue{
			"lockId": {
				S: aws.String(lockId),
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
