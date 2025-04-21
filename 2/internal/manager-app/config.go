package managerapp

import (
	"io"
	"time"

	"gopkg.in/yaml.v2"
)

type yamlManagerConfig struct {
	RequestTimeout   uint `yaml:"request-timeout"`
	CrackHashTimeout uint `yaml:"crack-hash-timeout"`
	CacheSize        uint `yaml:"cache-size"`
	MaxFailureCount  uint `yaml:"max-failure-count"`

	RabbitMqTaskExchange   string `yaml:"rabbitmq-task-exchange"`
	RabbitMqResultExchange string `yaml:"rabbitmq-result-exchange"`
	RabbitMqResultQueue    string `yaml:"rabbitmq-result-queue"`
}

type ManagerConfig struct {
	RequestTimeout   time.Duration
	CrackHashTimeout time.Duration
	CacheSize        uint
	MaxFailureCount  uint

	RabbitMqTaskExchange   string
	RabbitMqResultExchange string
	RabbitMqResultQueue    string
}

func uintToDuration(x uint) time.Duration {
	return time.Duration(time.Microsecond * time.Duration(x))
}

func LoadManagerConfig(configReader io.Reader) (*ManagerConfig, error) {
	decoder := yaml.NewDecoder(configReader)

	yamlConf := yamlManagerConfig{}

	err := decoder.Decode(&yamlConf)
	if err != nil {
		return nil, err
	}

	res := ManagerConfig{
		RequestTimeout:   uintToDuration(yamlConf.RequestTimeout),
		CrackHashTimeout: uintToDuration(yamlConf.CrackHashTimeout),
		CacheSize:        yamlConf.CacheSize,
		MaxFailureCount:  yamlConf.MaxFailureCount,

		RabbitMqTaskExchange:   yamlConf.RabbitMqTaskExchange,
		RabbitMqResultExchange: yamlConf.RabbitMqResultExchange,
		RabbitMqResultQueue:    yamlConf.RabbitMqResultQueue,
	}

	return &res, nil
}
