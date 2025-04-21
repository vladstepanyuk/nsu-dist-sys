package main

import (
	"CrackHash/internal/worker-app"
	"log"
	"os"

	amqp "github.com/rabbitmq/amqp091-go"
	flag "github.com/spf13/pflag"
)

var (
	configFile  = flag.String("config", "", "path to config file in YAML format")
	port        = flag.Uint16("port", 0, "port to listen connections")
	rabbitMqUri = flag.String("rabbitmq_uri", "", "uri to connect to rabbitmq")
)

func main() {
	flag.Parse()

	if configFile == nil || *configFile == "" {
		log.Fatal("provide config file path")
	}

	if rabbitMqUri == nil || *rabbitMqUri == "" {
		log.Fatal("provide rabbit mq uri")
	}

	file, err := os.Open(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	cfg, err := workerapp.LoadWorkerConfig(file)
	if err != nil {
		log.Fatal(err)
	}

	wApp, err := workerapp.NewWorkerApp(cfg)
	if err != nil {
		log.Fatal(err)
	}

	conn, err := amqp.Dial(*rabbitMqUri)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMq: %e", err)
	}
	defer conn.Close()

	wApp.DoMain(*port, conn)
}
