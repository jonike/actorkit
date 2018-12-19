package google_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	gpubsub "cloud.google.com/go/pubsub"
	"github.com/gokit/actorkit"
	"github.com/gokit/actorkit/transit"
	"github.com/gokit/actorkit/transit/google"
	"github.com/gokit/actorkit/transit/internal/benches"
	"github.com/gokit/actorkit/transit/internal/encoders"
)

func TestKafka(t *testing.T) {
	publishers, err := google.NewPublisherFactory(context.Background(), google.PublisherConfig{
		ProjectID:          "",
		CreateMissingTopic: true,
		Marshaler: &google.PubSubMarshaler{
			Marshaler: encoders.NoAddressMarshaler{},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, publishers)

	subscribers, err := google.NewSubscriptionFactory(context.Background(), google.SubscriberConfig{
		ProjectID: "",
		Unmarshaler: &google.PubSubUnmarshaler{
			Unmarshaler: encoders.NoAddressUnmarshaler{},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, subscribers)

	factory := google.PubSubFactory(func(factory *google.PublisherFactory, topic string) (transit.Publisher, error) {
		return factory.Publisher(topic, &gpubsub.PublishSettings{})
	}, func(factory *google.SubscriptionFactory, topic string, id string, receiver transit.Receiver) (actorkit.Subscription, error) {
		return factory.Subscribe(topic, id, &gpubsub.SubscriptionConfig{}, receiver, func(_ error) google.Directive {
			return google.Nack
		})
	})(publishers, subscribers)

	benches.PubSubFactoryTestSuite(t, factory)
}