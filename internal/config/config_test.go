package config

import (
	"context"
	"strings"
	"testing"
)

func TestLoadReturnsErrorForMissingRequiredEnv(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("DB_PASSWORD", "password")

	_, err := load(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("secret resolver should not be called")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected error for missing required env")
	}
	if !strings.Contains(err.Error(), "INSTANCE_CONNECTION_NAME") {
		t.Fatalf("expected missing env error to mention INSTANCE_CONNECTION_NAME, got %v", err)
	}
}

func TestLoadReturnsErrorForInvalidTypedEnv(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "project:region:instance")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("CREATE_AHEAD_DAYS", "abc")

	_, err := load(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("secret resolver should not be called")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected error for invalid int env")
	}
	if !strings.Contains(err.Error(), "CREATE_AHEAD_DAYS") {
		t.Fatalf("expected invalid env error to mention CREATE_AHEAD_DAYS, got %v", err)
	}
}

func TestLoadResolvesDBPasswordFromSecretManager(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "project:region:instance")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("GCP_PROJECT_ID", "demo-project")
	t.Setenv("DB_PASSWORD_SECRET", "db-password")

	called := false
	cfg, err := load(context.Background(), func(_ context.Context, name string) (string, error) {
		called = true
		if name != "projects/demo-project/secrets/db-password/versions/latest" {
			t.Fatalf("unexpected secret resource: %s", name)
		}
		return "secret-value", nil
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !called {
		t.Fatal("expected secret resolver to be called")
	}
	if cfg.DBPassword != "secret-value" {
		t.Fatalf("expected resolved secret value, got %q", cfg.DBPassword)
	}
	if cfg.DBPasswordSecret != "projects/demo-project/secrets/db-password/versions/latest" {
		t.Fatalf("unexpected stored secret ref: %q", cfg.DBPasswordSecret)
	}
}

func TestLoadRejectsBothPasswordSources(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "project:region:instance")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("DB_PASSWORD", "plaintext")
	t.Setenv("DB_PASSWORD_SECRET", "projects/demo/secrets/db-password/versions/latest")

	_, err := load(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("secret resolver should not be called")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected error when both password sources are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadReadsDDLGuardrails(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "project:region:instance")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("DB_PASSWORD", "plaintext")
	t.Setenv("PARTITION_SPAN_DAYS", "7")
	t.Setenv("MAX_CREATE_PARTITIONS_PER_RUN", "12")
	t.Setenv("MAX_DROP_PARTITIONS_PER_RUN", "8")
	t.Setenv("MAX_REPAIR_PARTITIONS_PER_RUN", "5")
	t.Setenv("AUTO_REPAIR_FORWARD_GAPS", "true")

	cfg, err := load(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("secret resolver should not be called")
		return "", nil
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.PartitionSpanDays != 7 {
		t.Fatalf("expected PartitionSpanDays=7, got %d", cfg.PartitionSpanDays)
	}
	if cfg.MaxCreatePartitionsPerRun != 12 {
		t.Fatalf("expected MaxCreatePartitionsPerRun=12, got %d", cfg.MaxCreatePartitionsPerRun)
	}
	if cfg.MaxDropPartitionsPerRun != 8 {
		t.Fatalf("expected MaxDropPartitionsPerRun=8, got %d", cfg.MaxDropPartitionsPerRun)
	}
	if cfg.MaxRepairPartitionsPerRun != 5 {
		t.Fatalf("expected MaxRepairPartitionsPerRun=5, got %d", cfg.MaxRepairPartitionsPerRun)
	}
	if !cfg.AutoRepairForwardGaps {
		t.Fatal("expected AutoRepairForwardGaps=true")
	}
}

func TestLoadRejectsInvalidPartitionSpanDays(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "project:region:instance")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("DB_PASSWORD", "plaintext")
	t.Setenv("PARTITION_SPAN_DAYS", "0")

	_, err := load(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("secret resolver should not be called")
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "PARTITION_SPAN_DAYS must be >= 1") {
		t.Fatalf("expected invalid partition span error, got %v", err)
	}
}

func TestLoadRejectsInvalidRepairLimit(t *testing.T) {
	t.Setenv("INSTANCE_CONNECTION_NAME", "project:region:instance")
	t.Setenv("DB_USER", "user")
	t.Setenv("DB_NAME", "dbname")
	t.Setenv("TABLE_NAME", "events")
	t.Setenv("DB_PASSWORD", "plaintext")
	t.Setenv("MAX_REPAIR_PARTITIONS_PER_RUN", "0")

	_, err := load(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("secret resolver should not be called")
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "MAX_REPAIR_PARTITIONS_PER_RUN must be >= 1") {
		t.Fatalf("expected invalid repair limit error, got %v", err)
	}
}
