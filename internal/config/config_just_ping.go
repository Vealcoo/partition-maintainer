package config

import "context"

func LoadJustPing(ctx context.Context) (Config, error) {
	return loadJustPing(ctx, accessSecretValue)
}

func loadJustPing(ctx context.Context, secretResolver func(context.Context, string) (string, error)) (Config, error) {
	instanceConnectionName, err := requiredEnv("INSTANCE_CONNECTION_NAME")
	if err != nil {
		return Config{}, err
	}
	dbUser, err := requiredEnv("DB_USER")
	if err != nil {
		return Config{}, err
	}
	dbPassword, dbPasswordSecret, err := resolveDBPassword(ctx, secretResolver)
	if err != nil {
		return Config{}, err
	}
	dbName, err := requiredEnv("DB_NAME")
	if err != nil {
		return Config{}, err
	}
	dbPort, err := envInt("DB_PORT", 3306)
	if err != nil {
		return Config{}, err
	}

	return Config{
		InstanceConnectionName: instanceConnectionName,
		DBUser:                 dbUser,
		DBPassword:             dbPassword,
		DBPasswordSecret:       dbPasswordSecret,
		DBName:                 dbName,
		DBPort:                 dbPort,
	}, nil
}
