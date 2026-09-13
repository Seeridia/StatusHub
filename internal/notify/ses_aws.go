package notify

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type AWSSESClient struct{ client *sesv2.Client }

func NewAWSSESClient(client *sesv2.Client) *AWSSESClient { return &AWSSESClient{client: client} }

func (c *AWSSESClient) SendEmail(ctx context.Context, message SESMessage) (string, error) {
	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(message.From),
		Destination:      &types.Destination{ToAddresses: []string{message.To}},
		Content: &types.EmailContent{Simple: &types.Message{
			Subject: &types.Content{Data: aws.String(message.Subject), Charset: aws.String("UTF-8")},
			Body:    &types.Body{Text: &types.Content{Data: aws.String(message.Text), Charset: aws.String("UTF-8")}},
		}},
		EmailTags: []types.MessageTag{{Name: aws.String("delivery_id"), Value: aws.String(message.DeliveryID)}},
	}
	if message.ConfigurationSet != "" {
		input.ConfigurationSetName = aws.String(message.ConfigurationSet)
	}
	output, err := c.client.SendEmail(ctx, input)
	if err != nil {
		return "", err
	}
	return aws.ToString(output.MessageId), nil
}
