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
}

type ManagerConfig struct {
	RequestTimeout   time.Duration
	CrackHashTimeout time.Duration
	CacheSize        uint
	MaxFailureCount  uint
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
	}

	return &res, nil
}
