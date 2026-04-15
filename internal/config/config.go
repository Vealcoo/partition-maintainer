package config

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

type Config struct {
	InstanceConnectionName string
	DBUser                 string
	DBPassword             string
	DBPasswordSecret       string
	DBName                 string
	DBPort                 int

	TableName       string
	PartitionColumn string
	Timezone        string

	PartitionSpanDays int
	CreateAheadDays   int
	DropBeforeDays    int

	MaxCreatePartitionsPerRun int
	MaxDropPartitionsPerRun   int
	MaxRepairPartitionsPerRun int

	AutoRepairForwardGaps bool

	LockName           string
	LockTimeoutSeconds int

	DryRun bool
}

func Load(ctx context.Context) (Config, error) {
	return load(ctx, accessSecretValue)
}

func load(ctx context.Context, secretResolver func(context.Context, string) (string, error)) (Config, error) {
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
	tableName, err := requiredEnv("TABLE_NAME")
	if err != nil {
		return Config{}, err
	}
	createAheadDays, err := envInt("CREATE_AHEAD_DAYS", 30)
	if err != nil {
		return Config{}, err
	}
	partitionSpanDays, err := envInt("PARTITION_SPAN_DAYS", 1)
	if err != nil {
		return Config{}, err
	}
	dropBeforeDays, err := envInt("DROP_BEFORE_DAYS", 90)
	if err != nil {
		return Config{}, err
	}
	maxCreatePartitionsPerRun, err := envInt("MAX_CREATE_PARTITIONS_PER_RUN", 30)
	if err != nil {
		return Config{}, err
	}
	maxDropPartitionsPerRun, err := envInt("MAX_DROP_PARTITIONS_PER_RUN", 30)
	if err != nil {
		return Config{}, err
	}
	maxRepairPartitionsPerRun, err := envInt("MAX_REPAIR_PARTITIONS_PER_RUN", 30)
	if err != nil {
		return Config{}, err
	}
	lockTimeoutSeconds, err := envInt("LOCK_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	autoRepairForwardGaps, err := envBool("AUTO_REPAIR_FORWARD_GAPS", false)
	if err != nil {
		return Config{}, err
	}
	dryRun, err := envBool("DRY_RUN", false)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		InstanceConnectionName: instanceConnectionName,
		DBUser:                 dbUser,
		DBPassword:             dbPassword,
		DBPasswordSecret:       dbPasswordSecret,
		DBName:                 dbName,
		DBPort:                 dbPort,
		TableName:              tableName,
		PartitionColumn:        getEnv("PARTITION_COLUMN", "event_date"),
		Timezone:               getEnv("TIMEZONE", "Asia/Taipei"),
		PartitionSpanDays:      partitionSpanDays,
		CreateAheadDays:        createAheadDays,
		DropBeforeDays:         dropBeforeDays,
		MaxCreatePartitionsPerRun: maxCreatePartitionsPerRun,
		MaxDropPartitionsPerRun:   maxDropPartitionsPerRun,
		MaxRepairPartitionsPerRun: maxRepairPartitionsPerRun,
		AutoRepairForwardGaps:     autoRepairForwardGaps,
		LockTimeoutSeconds:     lockTimeoutSeconds,
		DryRun:                 dryRun,
	}

	if cfg.PartitionSpanDays < 1 {
		return Config{}, fmt.Errorf("PARTITION_SPAN_DAYS must be >= 1")
	}
	if cfg.CreateAheadDays < 1 {
		return Config{}, fmt.Errorf("CREATE_AHEAD_DAYS must be >= 1")
	}
	if cfg.DropBeforeDays < 0 {
		return Config{}, fmt.Errorf("DROP_BEFORE_DAYS must be >= 0")
	}
	if cfg.MaxCreatePartitionsPerRun < 1 {
		return Config{}, fmt.Errorf("MAX_CREATE_PARTITIONS_PER_RUN must be >= 1")
	}
	if cfg.MaxDropPartitionsPerRun < 1 {
		return Config{}, fmt.Errorf("MAX_DROP_PARTITIONS_PER_RUN must be >= 1")
	}
	if cfg.MaxRepairPartitionsPerRun < 1 {
		return Config{}, fmt.Errorf("MAX_REPAIR_PARTITIONS_PER_RUN must be >= 1")
	}
	if cfg.LockTimeoutSeconds < 0 {
		return Config{}, fmt.Errorf("LOCK_TIMEOUT_SECONDS must be >= 0")
	}

	cfg.LockName = getEnv("PARTITION_LOCK_NAME", fmt.Sprintf("partition-maintainer:%s", cfg.TableName))

	return cfg, nil
}

func resolveDBPassword(ctx context.Context, secretResolver func(context.Context, string) (string, error)) (string, string, error) {
	dbPassword := strings.TrimSpace(os.Getenv("DB_PASSWORD"))
	dbPasswordSecret := strings.TrimSpace(os.Getenv("DB_PASSWORD_SECRET"))

	switch {
	case dbPassword != "" && dbPasswordSecret != "":
		return "", "", fmt.Errorf("DB_PASSWORD and DB_PASSWORD_SECRET are mutually exclusive")
	case dbPasswordSecret != "":
		secretResource, err := normalizeSecretResource(dbPasswordSecret, strings.TrimSpace(os.Getenv("GCP_PROJECT_ID")))
		if err != nil {
			return "", "", err
		}

		password, err := secretResolver(ctx, secretResource)
		if err != nil {
			return "", "", fmt.Errorf("resolve DB_PASSWORD_SECRET: %w", err)
		}

		return password, secretResource, nil
	case dbPassword != "":
		return dbPassword, "", nil
	default:
		return "", "", fmt.Errorf("missing required env: DB_PASSWORD or DB_PASSWORD_SECRET")
	}
}

func normalizeSecretResource(secretRef, projectID string) (string, error) {
	switch {
	case secretRef == "":
		return "", fmt.Errorf("DB_PASSWORD_SECRET must not be empty")
	case strings.HasPrefix(secretRef, "projects/"):
		return secretRef, nil
	case strings.Contains(secretRef, "/"):
		return "", fmt.Errorf("DB_PASSWORD_SECRET must be a full resource name or a bare secret id")
	case projectID == "":
		return "", fmt.Errorf("GCP_PROJECT_ID is required when DB_PASSWORD_SECRET is not a full resource name")
	default:
		return fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretRef), nil
	}
}

func requiredEnv(key string) (string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return "", fmt.Errorf("missing required env: %s", key)
	}
	return v, nil
}

func getEnv(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func envInt(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid int env %s=%q: %w", key, v, err)
	}
	return n, nil
}

func envBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid bool env %s=%q: %w", key, v, err)
	}
	return b, nil
}

func accessSecretValue(ctx context.Context, name string) (string, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("create secret manager client: %w", err)
	}
	defer client.Close()

	resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	})
	if err != nil {
		return "", fmt.Errorf("access secret version %q: %w", name, err)
	}

	return strings.TrimSpace(string(resp.Payload.Data)), nil
}
