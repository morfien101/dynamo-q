package queue

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws/session"
)

func NewStore(ctx context.Context, backend, queueTableName, gcpProject string) (Store, error) {
	selected := strings.ToLower(strings.TrimSpace(backend))
	if selected == "" {
		selected = "aws"
	}

	switch selected {
	case "aws", "dynamodb":
		sess := session.Must(session.NewSession())
		return NewDynamoDBStore(sess, queueTableName), nil
	case "gcp", "firestore":
		projectID := strings.TrimSpace(gcpProject)
		if projectID == "" {
			projectID = strings.TrimSpace(os.Getenv("GCP_PROJECT"))
		}
		if projectID == "" {
			projectID = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
		}
		if projectID == "" {
			return nil, fmt.Errorf("gcp project ID is required when backend is set to gcp/firestore")
		}
		return NewFirestoreStore(ctx, projectID, queueTableName)
	default:
		return nil, fmt.Errorf("unsupported backend: %s", backend)
	}
}
