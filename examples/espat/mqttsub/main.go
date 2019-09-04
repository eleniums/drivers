// This is a sensor station that uses a ESP8266 or ESP32 running on the device UART1.
// It creates an MQTT connection that publishes a message every second
// to an MQTT broker.
//
// In other words:
// Your computer <--> UART0 <--> MCU <--> UART1 <--> ESP8266 <--> Internet <--> MQTT broker.
//
// You must install the Paho MQTT package to build this program:
//
// 		go get -u github.com/eclipse/paho.mqtt.golang
//
package main

import (
	"machine"
	"math/rand"
	"time"

	"tinygo.org/x/drivers/espat"
	"tinygo.org/x/drivers/espat/mqtt"
)

// access point info
const ssid = "ssid"
const pass = "pwd"

// IP address of the MQTT broker to use. Replace with your own info.
//const server = "tcp://test.mosquitto.org:1883"
const server = "tcp://10.0.0.17:1883"

//const server = "ssl://test.mosquitto.org:8883"

// change these to connect to a different UART or pins for the ESP8266/ESP32
var (
	// these are defaults for the Arduino Nano33 IoT.
	uart = machine.UART1
	tx   = machine.PA22
	rx   = machine.PA23

	console = machine.UART0

	adaptor *espat.Device
	cl      mqtt.Client
	topic   = "tinygo"
)

func subHandler(client mqtt.Client, msg mqtt.Message) {
	println("subHandler function")
	// fmt.Printf("[%s]  ", msg.Topic())
	// fmt.Printf("%s\n", msg.Payload())
}

func main() {
	time.Sleep(3000 * time.Millisecond)

	uart.Configure(machine.UARTConfig{TX: tx, RX: rx})
	rand.Seed(time.Now().UnixNano())

	// Init esp8266/esp32
	adaptor = espat.New(uart)
	adaptor.Configure()

	// first check if connected
	if connectToESP() {
		println("Connected to wifi adaptor.")
		adaptor.Echo(false)

		connectToAP()
	} else {
		println("")
		failMessage("Unable to connect to wifi adaptor.")
		return
	}

	opts := mqtt.NewClientOptions(adaptor)
	opts.AddBroker(server).SetClientID("tinygo-client-" + randomString(10))

	println("Connecting to MQTT...")
	cl = mqtt.NewClient(opts)
	if token := cl.Connect(); token.Wait() && token.Error() != nil {
		failMessage(token.Error().Error())
	}
	time.Sleep(500 * time.Millisecond)

	// subscribe
	token := cl.Subscribe(topic, 0, subHandler)
	token.Wait()
	if token.Error() != nil {
		failMessage(token.Error().Error())
	}
	time.Sleep(500 * time.Millisecond)

	//go publishing()
	startPrinting()

	select {}

	// Right now this code is never reached. Need a way to trigger it...
	println("Disconnecting MQTT...")
	cl.Disconnect(100)

	println("Done.")
}

func startPrinting() {
	go printing()
}

func printing() {
	for {
		println("waiting...")
		time.Sleep(2 * time.Second)
	}
}

func publishing() {
	for {
		println("Publishing MQTT message...")
		data := []byte("{\"e\":[{ \"n\":\"hello\", \"v\":101 }]}")
		token := cl.Publish(topic, 0, false, data)
		token.Wait()
		if token.Error() != nil {
			println(token.Error().Error())
		}

		time.Sleep(1000 * time.Millisecond)
	}
}

// connect to ESP8266/ESP32
func connectToESP() bool {
	for i := 0; i < 5; i++ {
		println("Connecting to wifi adaptor...")
		if adaptor.Connected() {
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// connect to access point
func connectToAP() {
	println("Connecting to wifi network...")

	adaptor.SetWifiMode(espat.WifiModeClient)
	adaptor.ConnectToAP(ssid, pass, 10)

	println("Connected.")
	println(adaptor.GetClientIP())
}

// Returns an int >= min, < max
func randomInt(min, max int) int {
	return min + rand.Intn(max-min)
}

// Generate a random string of A-Z chars with len = l
func randomString(len int) string {
	bytes := make([]byte, len)
	for i := 0; i < len; i++ {
		bytes[i] = byte(randomInt(65, 90))
	}
	return string(bytes)
}

func failMessage(msg string) {
	for {
		println(msg)
		time.Sleep(1 * time.Second)
	}
}
