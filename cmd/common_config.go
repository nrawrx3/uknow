package cmdcommon

import "github.com/kelseyhightower/envconfig"

type CommonConfig struct {
	AESKey          string `split_words:"true"`
	EncryptMessages bool   `split_words:"true"`
}

func LoadCommonConfig() (*CommonConfig, error) {
	var c CommonConfig
	err := envconfig.Process("", &c)
	if err != nil {
		return nil, err
	}
	return &c, err
}
