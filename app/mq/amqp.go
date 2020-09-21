package mq

import (
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/xeipuuv/gojsonschema"
	"mq-subscriber/app/actions"
	"mq-subscriber/app/logging"
	"mq-subscriber/app/schema"
	"mq-subscriber/app/types"
	"mq-subscriber/app/utils"
	"time"
)

type AmqpDrive struct {
	url             string
	schema          *schema.Schema
	logging         *logging.Logging
	conn            *amqp.Connection
	notifyConnClose chan *amqp.Error
	channel         *utils.SyncChannel
	channelDone     *utils.SyncChannelDone
	channelReady    *utils.SyncChannelReady
	notifyChanClose *utils.SyncNotifyChanClose
}

func NewAmqpDrive(url string, schema *schema.Schema, logging *logging.Logging) (session *AmqpDrive, err error) {
	session = new(AmqpDrive)
	session.url = url
	session.schema = schema
	session.logging = logging
	conn, err := amqp.Dial(url)
	if err != nil {
		return
	}
	session.conn = conn
	session.notifyConnClose = make(chan *amqp.Error)
	conn.NotifyClose(session.notifyConnClose)
	go session.listenConn()
	session.channel = utils.NewSyncChannel()
	session.channelDone = utils.NewSyncChannelDone()
	session.channelReady = utils.NewSyncChannelReady()
	session.notifyChanClose = utils.NewSyncNotifyChanClose()
	return
}

func (c *AmqpDrive) listenConn() {
	select {
	case <-c.notifyConnClose:
		logrus.Error("AMQP connection has been disconnected")
		c.reconnected()
	}
}

func (c *AmqpDrive) reconnected() {
	count := 0
	for {
		time.Sleep(time.Second * 5)
		count++
		logrus.Info("Trying to reconnect:", count)
		conn, err := amqp.Dial(c.url)
		if err != nil {
			logrus.Error(err)
			continue
		}
		c.conn = conn
		c.notifyConnClose = make(chan *amqp.Error)
		conn.NotifyClose(c.notifyConnClose)
		go c.listenConn()
		logrus.Info("Attempt to reconnect successfully")
		break
	}
}

func (c *AmqpDrive) SetChannel(ID string) (err error) {
	var channel *amqp.Channel
	channel, err = c.conn.Channel()
	if err != nil {
		return
	}
	c.channel.Set(ID, channel)
	c.channelDone.Set(ID, make(chan int))
	notifyChanClose := make(chan *amqp.Error)
	channel.NotifyClose(notifyChanClose)
	c.notifyChanClose.Set(ID, notifyChanClose)
	go c.listenChannel(ID)
	return
}

func (c *AmqpDrive) listenChannel(ID string) {
	select {
	case <-c.notifyChanClose.Get(ID):
		logrus.Error("Channel connection is disconnected:", ID)
		if c.channelReady.Get(ID) {
			c.refreshChannel(ID)
		} else {
			break
		}
	case <-c.channelDone.Get(ID):
		break
	}
}

func (c *AmqpDrive) refreshChannel(ID string) {
	for {
		err := c.SetChannel(ID)
		if err != nil {
			continue
		}
		option, err := c.schema.Get(ID)
		if err != nil {
			continue
		}
		err = c.SetConsume(option)
		if err != nil {
			if c.channelReady.Get(ID) {
				continue
			} else {
				break
			}
		}
		logrus.Info("Channel refresh successfully")
		break
	}
}

func (c *AmqpDrive) CloseChannel(ID string) error {
	c.channelDone.Get(ID) <- 1
	return c.channel.Get(ID).Close()
}

func (c *AmqpDrive) SetConsume(option types.SubscriberOption) (err error) {
	msgs, err := c.channel.Get(option.Identity).Consume(
		option.Queue,
		option.Identity,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		c.channelReady.Set(option.Identity, false)
		return
	}
	c.channelReady.Set(option.Identity, true)
	go func() {
		for d := range msgs {
			body, errs := actions.Fetch(types.FetchOption{
				Url:    option.Url,
				Secret: option.Secret,
				Body:   string(d.Body),
			})
			var message map[string]interface{}
			var bodyRecord interface{}
			if jsoniter.Valid(d.Body) {
				jsoniter.Unmarshal(d.Body, &bodyRecord)
			} else {
				d.Nack(false, false)
				return
			}
			if len(errs) != 0 {
				msg := make([]string, len(errs))
				for index, value := range errs {
					msg[index] = value.Error()
				}
				message = map[string]interface{}{
					"Identity": option.Identity,
					"Queue":    option.Queue,
					"Url":      option.Url,
					"Secret":   option.Secret,
					"Body":     bodyRecord,
					"Status":   false,
					"Response": map[string]interface{}{
						"errs": msg,
					},
					"Time": time.Now().Unix(),
				}
				d.Nack(false, false)
			} else {
				var responseRecord interface{}
				result, err := gojsonschema.Validate(
					gojsonschema.NewBytesLoader([]byte(`{"type":"object"}`)),
					gojsonschema.NewBytesLoader(body),
				)
				if err != nil {
					responseRecord = map[string]interface{}{
						"raw": string(body),
					}
				} else {
					if result.Valid() {
						jsoniter.Unmarshal(body, &responseRecord)
					} else {
						responseRecord = map[string]interface{}{
							"raw": string(body),
						}
					}
				}
				message = map[string]interface{}{
					"Identity": option.Identity,
					"Queue":    option.Queue,
					"Url":      option.Url,
					"Secret":   option.Secret,
					"Body":     bodyRecord,
					"Status":   true,
					"Response": responseRecord,
					"Time":     time.Now().Unix(),
				}
				d.Ack(false)
			}
			c.logging.Push(&types.LoggingPush{
				Identity: option.Identity,
				HasError: len(errs) != 0,
				Message:  message,
			})
		}
	}()
	return
}