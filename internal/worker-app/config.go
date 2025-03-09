package workerapp

import (
	"io"
	"time"

	"gopkg.in/yaml.v2"
)

type yamlWorkerConfig struct {
	RequestTimeout   uint   `yaml:"request-timeout"`
	CacheSize        uint   `yaml:"cache-size"`
	ManagerAddress   string `yaml:"manager-address"`
	CrackHashTimeout uint   `yaml:"crack-hash-timeout"`
}

type WorkerConfig struct {
	RequestTimeout   time.Duration
	CrackHashTimeout time.Duration
	CacheSize        uint
	ManagerAddress   string
}

func uintToDuration(x uint) time.Duration {
	return time.Duration(time.Microsecond * time.Duration(x))
}

func LoadWorkerConfig(configReader io.Reader) (*WorkerConfig, error) {
	decoder := yaml.NewDecoder(configReader)

	yamlCfg := yamlWorkerConfig{}

	err := decoder.Decode(&yamlCfg)
	if err != nil {
		return nil, err
	}

	return &WorkerConfig{
		RequestTimeout:   uintToDuration(yamlCfg.RequestTimeout),
		CrackHashTimeout: uintToDuration(yamlCfg.CrackHashTimeout),
		CacheSize:        yamlCfg.CacheSize,
		ManagerAddress:   yamlCfg.ManagerAddress,
	}, nil
}
