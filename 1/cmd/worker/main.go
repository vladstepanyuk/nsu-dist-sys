package main

import (
	"CrackHash/internal/worker-app"
	"log"
	"os"

	flag "github.com/spf13/pflag"
)

var (
	configFile = flag.String("config", "", "path to config file in YAML format")
	port       = flag.Uint16("port", 0, "port to listen connections")
)

func main() {
	flag.Parse()

	if configFile == nil || *configFile == "" {
		log.Fatal("provide config file path")
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

	wApp.DoMain(*port)
}
