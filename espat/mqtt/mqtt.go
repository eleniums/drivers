package mqtt

import (
	"errors"
	"strings"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
	"tinygo.org/x/drivers/espat"
	"tinygo.org/x/drivers/espat/net"
	"tinygo.org/x/drivers/espat/tls"
)

// NewClient will create an MQTT v3.1.1 client with all of the options specified
// in the provided ClientOptions. The client must have the Connect method called
// on it before it may be used. This is to make sure resources (such as a net
// connection) are created before the application is actually ready.
func NewClient(o *ClientOptions) Client {
	c := &mqttclient{opts: o, adaptor: o.Adaptor}
	c.msgRouter, c.stopRouter = newRouter()
	return c
}

type mqttclient struct {
	adaptor         *espat.Device
	conn            net.Conn
	connected       bool
	opts            *ClientOptions
	mid             uint16
	inbound         chan packets.ControlPacket
	stop            chan struct{}
	msgRouter       *router
	stopRouter      chan bool
	incomingPubChan chan *packets.PublishPacket
}

// AddRoute allows you to add a handler for messages on a specific topic
// without making a subscription. For example having a different handler
// for parts of a wildcard subscription
func (c *mqttclient) AddRoute(topic string, callback MessageHandler) {
	return
}

// IsConnected returns a bool signifying whether
// the client is connected or not.
func (c *mqttclient) IsConnected() bool {
	return c.connected
}

// IsConnectionOpen return a bool signifying whether the client has an active
// connection to mqtt broker, i.e not in disconnected or reconnect mode
func (c *mqttclient) IsConnectionOpen() bool {
	return c.connected
}

// Connect will create a connection to the message broker.
func (c *mqttclient) Connect() Token {
	println("Connect()")

	var err error

	// make connection
	if strings.Contains(c.opts.Servers, "ssl://") {
		url := strings.TrimPrefix(c.opts.Servers, "ssl://")
		c.conn, err = tls.Dial("tcp", url, nil)
		if err != nil {
			return &mqtttoken{err: err}
		}
	} else if strings.Contains(c.opts.Servers, "tcp://") {
		url := strings.TrimPrefix(c.opts.Servers, "tcp://")
		c.conn, err = net.Dial("tcp", url)
		if err != nil {
			return &mqtttoken{err: err}
		}
	} else {
		// invalid protocol
		return &mqtttoken{err: errors.New("invalid protocol")}
	}

	c.inbound = make(chan packets.ControlPacket)
	c.stop = make(chan struct{})
	c.incomingPubChan = make(chan *packets.PublishPacket)
	//c.msgRouter.matchAndDispatch(c.incomingPubChan, c.opts.Order, c)

	// send the MQTT connect message
	connectPkt := packets.NewControlPacket(packets.Connect).(*packets.ConnectPacket)
	connectPkt.Qos = 0
	if c.opts.Username != "" {
		connectPkt.Username = c.opts.Username
		connectPkt.UsernameFlag = true
	}

	if c.opts.Password != "" {
		connectPkt.Password = []byte(c.opts.Password)
		connectPkt.PasswordFlag = true
	}

	connectPkt.ClientIdentifier = c.opts.ClientID
	connectPkt.ProtocolVersion = byte(c.opts.ProtocolVersion)
	connectPkt.ProtocolName = "MQTT"
	connectPkt.Keepalive = 30

	err = connectPkt.Write(c.conn)
	if err != nil {
		return &mqtttoken{err: err}
	}

	// TODO: handle timeout
CONNECT:
	for {
		packet, _ := packets.ReadPacket(c.conn)

		if packet != nil {
			ack, ok := packet.(*packets.ConnackPacket)
			if ok {
				if ack.ReturnCode == 0 {
					// success
					c.connected = true

					// start processing messages
					break CONNECT
				}
				// otherwise something went wrong
				println("error", packet.String())
				return &mqtttoken{err: errors.New(packet.String())}
			}
		}

		println("Retry connect")
		time.Sleep(500 * time.Millisecond)
	}

	go incoming(c)
	go processInbound(c)

	c.connected = true
	return &mqtttoken{}
}

// Disconnect will end the connection with the server, but not before waiting
// the specified number of milliseconds to wait for existing work to be
// completed.
func (c *mqttclient) Disconnect(quiesce uint) {
	c.conn.Close()
	return
}

// Publish will publish a message with the specified QoS and content
// to the specified topic.
// Returns a token to track delivery of the message to the broker
func (c *mqttclient) Publish(topic string, qos byte, retained bool, payload interface{}) Token {
	pub := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
	pub.Qos = qos
	pub.TopicName = topic
	switch payload.(type) {
	case string:
		pub.Payload = []byte(payload.(string))
	case []byte:
		pub.Payload = payload.([]byte)
	default:
		return &mqtttoken{err: errors.New("Unknown payload type")}
	}
	pub.MessageID = c.mid
	c.mid++

	err := pub.Write(c.conn)
	return &mqtttoken{err: err}
}

// Subscribe starts a new subscription. Provide a MessageHandler to be executed when
// a message is published on the topic provided.
func (c *mqttclient) Subscribe(topic string, qos byte, callback MessageHandler) Token {
	if !c.IsConnected() {
		return &mqtttoken{err: errors.New("MQTT client not connected")}
	}

	sub := packets.NewControlPacket(packets.Subscribe).(*packets.SubscribePacket)
	sub.Topics = append(sub.Topics, topic)
	sub.Qoss = append(sub.Qoss, qos)

	if callback != nil {
		c.msgRouter.addRoute(topic, callback)
	}

	sub.MessageID = c.mid
	c.mid++

	err := sub.Write(c.conn)
	return &mqtttoken{err: err}
}

// SubscribeMultiple starts a new subscription for multiple topics. Provide a MessageHandler to
// be executed when a message is published on one of the topics provided.
func (c *mqttclient) SubscribeMultiple(filters map[string]byte, callback MessageHandler) Token {
	return &mqtttoken{}
}

// Unsubscribe will end the subscription from each of the topics provided.
// Messages published to those topics from other clients will no longer be
// received.
func (c *mqttclient) Unsubscribe(topics ...string) Token {
	return &mqtttoken{}
}

// OptionsReader returns a ClientOptionsReader which is a copy of the clientoptions
// in use by the client.
func (c *mqttclient) OptionsReader() ClientOptionsReader {
	r := ClientOptionsReader{}
	return r
}

func processInbound(c *mqttclient) {
	for {
		println("logic waiting for msg on ibound")

		select {
		case msg := <-c.inbound:

			switch m := msg.(type) {
			case *packets.PingrespPacket:
				//println("received pingresp")
				//atomic.StoreInt32(&c.pingOutstanding, 0)
			case *packets.SubackPacket:
				// println("received suback,", m.MessageID)
				// token := c.getToken(m.MessageID)
				// switch t := token.(type) {
				// case *SubscribeToken:
				// 	DEBUG.Println(NET, "granted qoss", m.ReturnCodes)
				// 	for i, qos := range m.ReturnCodes {
				// 		t.subResult[t.subs[i]] = qos
				// 	}
				// }
				// token.flowComplete()
				// c.freeID(m.MessageID)
			case *packets.UnsubackPacket:
				// DEBUG.Println(NET, "received unsuback, id:", m.MessageID)
				// c.getToken(m.MessageID).flowComplete()
				// c.freeID(m.MessageID)
			case *packets.PublishPacket:
				println("packet!!")
				c.incomingPubChan <- m
				// DEBUG.Println(NET, "received publish, msgId:", m.MessageID)
				// DEBUG.Println(NET, "putting msg on onPubChan")
				// switch m.Qos {
				// case 2:
				// 	c.incomingPubChan <- m
				// 	DEBUG.Println(NET, "done putting msg on incomingPubChan")
				// case 1:
				// 	c.incomingPubChan <- m
				// 	DEBUG.Println(NET, "done putting msg on incomingPubChan")
				// case 0:
				// 	select {
				// 	case c.incomingPubChan <- m:
				// 	case <-c.stop:
				// 	}
				// 	DEBUG.Println(NET, "done putting msg on incomingPubChan")
				// }
			case *packets.PubackPacket:
				// DEBUG.Println(NET, "received puback, id:", m.MessageID)
				// // c.receipts.get(msg.MsgId()) <- Receipt{}
				// // c.receipts.end(msg.MsgId())
				// c.getToken(m.MessageID).flowComplete()
				// c.freeID(m.MessageID)
			case *packets.PubrecPacket:
				// DEBUG.Println(NET, "received pubrec, id:", m.MessageID)
				// prel := packets.NewControlPacket(packets.Pubrel).(*packets.PubrelPacket)
				// prel.MessageID = m.MessageID
				// select {
				// case c.oboundP <- &PacketAndToken{p: prel, t: nil}:
				// case <-c.stop:
				// }
			case *packets.PubrelPacket:
				// DEBUG.Println(NET, "received pubrel, id:", m.MessageID)
				// pc := packets.NewControlPacket(packets.Pubcomp).(*packets.PubcompPacket)
				// pc.MessageID = m.MessageID
				// persistOutbound(c.persist, pc)
				// select {
				// case c.oboundP <- &PacketAndToken{p: pc, t: nil}:
				// case <-c.stop:
				// }
			case *packets.PubcompPacket:
				// DEBUG.Println(NET, "received pubcomp, id:", m.MessageID)
				// c.getToken(m.MessageID).flowComplete()
				// c.freeID(m.MessageID)
			}
		case <-c.stop:
			println("logic stopped")
			return
		}
	}
}

// read incoming messages off the wire, then send into inbound channel.
func incoming(c *mqttclient) {
	var err error
	var cp packets.ControlPacket

	println("incoming started")

	for {
		println("aqui")
		if cp, err = packets.ReadPacket(c.conn); err != nil {
			println("bail out")
			break
		}
		if cp != nil {
			println("Received Message")
			select {
			case c.inbound <- cp:
				// Notify keepalive logic that we recently received a packet
				// if c.options.KeepAlive != 0 {
				// 	c.lastReceived.Store(time.Now())
				// }
			case <-c.stop:
				// This avoids a deadlock should a message arrive while shutting down.
				// In that case the "reader" of c.ibound might already be gone
				println("incoming dropped a received message during shutdown")
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// We received an error on read.
	// If disconnect is in progress, swallow error and return
	select {
	case <-c.stop:
		println("incoming stopped")
		return
	// Not trying to disconnect, send the error to the errors channel
	default:
		println("incoming stopped with error", err.Error())
		//signalError(c.errors, err)
		return
	}
}

func (c *mqttclient) ackFunc(packet *packets.PublishPacket) func() {
	return func() {
		switch packet.Qos {
		case 2:
			// pr := packets.NewControlPacket(packets.Pubrec).(*packets.PubrecPacket)
			// pr.MessageID = packet.MessageID
			// DEBUG.Println(NET, "putting pubrec msg on obound")
			// select {
			// case c.oboundP <- &PacketAndToken{p: pr, t: nil}:
			// case <-c.stop:
			// }
			// DEBUG.Println(NET, "done putting pubrec msg on obound")
		case 1:
			// pa := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
			// pa.MessageID = packet.MessageID
			// DEBUG.Println(NET, "putting puback msg on obound")
			// persistOutbound(c.persist, pa)
			// select {
			// case c.oboundP <- &PacketAndToken{p: pa, t: nil}:
			// case <-c.stop:
			// }
			// DEBUG.Println(NET, "done putting puback msg on obound")
		case 0:
			// do nothing, since there is no need to send an ack packet back
		}
	}
}
