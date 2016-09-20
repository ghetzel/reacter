package reacter

import (
	"fmt"
	"github.com/streadway/amqp"
)

const (
	DEFAULT_AMQP_PORT  = 5672
	DEFAULT_QUEUE_NAME = `reacter`
)

type Consumer struct {
	ID        string
	Host      string
	Port      int
	Username  string
	Password  string
	Vhost     string
	QueueName string

	Durable    bool
	Autodelete bool
	Exclusive  bool

	conn    *amqp.Connection
	channel *amqp.Channel
	queue   amqp.Queue
	uri     amqp.URI
}

func NewConsumer(uri string) (*Consumer, error) {
	c := new(Consumer)

	if u, err := amqp.ParseURI(uri); err == nil {
		c.uri = u
		c.Host = u.Host
		c.Port = u.Port
		c.Username = u.Username
		c.Password = u.Password
		c.Vhost = u.Vhost
		c.QueueName = DEFAULT_QUEUE_NAME

		return c, nil
	} else {
		return nil, err
	}
}

func (self *Consumer) Close() error {
	if self.conn == nil {
		return fmt.Errorf("Cannot close, connection does not exist")
	}

	return self.conn.Close()
}

func (self *Consumer) Connect() error {
	if conn, err := amqp.Dial(self.uri.String()); err == nil {
		self.conn = conn

		if channel, err := self.conn.Channel(); err == nil {
			self.channel = channel

			if queue, err := self.channel.QueueDeclare(self.QueueName, self.Durable, self.Autodelete, self.Exclusive, false, nil); err == nil {
				self.queue = queue
				return nil
			} else {
				defer self.channel.Close()
				return err
			}
		} else {
			defer self.conn.Close()
			return err
		}
	} else {
		return err
	}

	return nil
}

func (self *Consumer) SubscribeRaw() (<-chan amqp.Delivery, error) {
	return self.channel.Consume(self.queue.Name, self.ID, true, self.Exclusive, false, false, nil)
}

func (self *Consumer) Subscribe() (<-chan string, error) {
	output := make(chan string)

	if msgs, err := self.SubscribeRaw(); err == nil {
		go func() {
			for delivery := range msgs {
				output <- string(delivery.Body[:])
			}
		}()
	} else {
		return nil, err
	}

	return output, nil
}
