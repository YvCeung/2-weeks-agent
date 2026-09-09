// Package config 定义配置结构体并从 yaml + 环境变量加载。
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config 是应用的配置。目前只需要大模型的 base_url / api_key / model。
type Config struct {
	LLM LLMConfig `mapstructure:"llm"`
}

type LLMConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// Load 从 configs/config.yaml(或 CONFIG_PATH)加载配置,并允许环境变量覆盖。
func Load() (*Config, error) {
	v := viper.New()
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		v.SetConfigFile(p)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
	}

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		// 配置文件缺失时允许仅用默认值 + 环境变量启动
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &c, nil
}

// setDefaults 设定默认值,与 main.go 之前硬编码的行为保持一致。
func setDefaults(v *viper.Viper) {
	v.SetDefault("llm.base_url", "https://api.deepseek.com")
	v.SetDefault("llm.model", "deepseek-v4-pro")
}
