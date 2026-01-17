package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Server   ServerConfig
	Storage  StorageConfig
	Postgres PostgresConfig
	S3       S3Config
}

type StorageConfig struct {
	// Any future storage configurations can be added here
}

type ServerConfig struct {
	Port int
}

type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

type S3Config struct {
	Region          string
	Bucket          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
}

func LoadFromEnv() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Port: getEnvInt("PORT", 8080),
		},
		Storage: StorageConfig{},
		Postgres: PostgresConfig{
			Host:     getEnvString("POSTGRES_HOST", "localhost"),
			Port:     getEnvInt("POSTGRES_PORT", 5432),
			User:     getEnvString("POSTGRES_USER", "postgres"),
			Password: getEnvString("POSTGRES_PASSWORD", "postgres"),
			Database: getEnvString("POSTGRES_DB", "minilambda"),
			SSLMode:  getEnvString("POSTGRES_SSL_MODE", "disable"),
		},
		S3: S3Config{
			Region:          getEnvString("AWS_REGION", "us-east-1"),
			Bucket:          getEnvString("S3_BUCKET", "mini-lambda-functions"),
			Endpoint:        getEnvString("S3_ENDPOINT", ""),
			AccessKeyID:     getEnvString("AWS_ACCESS_KEY_ID", ""),
			SecretAccessKey: getEnvString("AWS_SECRET_ACCESS_KEY", ""),
			Prefix:          getEnvString("S3_PREFIX", "functions"),
		},
	}

	if cfg.S3.Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required")
	}

	return cfg, nil
}

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// func getEnvBool(key string, defaultValue bool) bool {
// 	if value := os.Getenv(key); value != "" {
// 		return value == "true"
// 	}
// 	return defaultValue
// }
