package db

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	mysqlDriver "github.com/go-sql-driver/mysql"

	"partition-maintainer/internal/config"
)

var registerDialOnce sync.Once

func OpenMySQLWithConnector(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	dialer, err := cloudsqlconn.NewDialer(
		ctx,
		cloudsqlconn.WithDefaultDialOptions(cloudsqlconn.WithPrivateIP()),
	)
	if err != nil {
		return nil, fmt.Errorf("create cloud sql dialer: %w", err)
	}

	registerDialOnce.Do(func() {
		mysqlDriver.RegisterDialContext("cloudsql-mysql", func(ctx context.Context, _ string) (net.Conn, error) {
			return dialer.Dial(ctx, cfg.InstanceConnectionName)
		})
	})

	dsn := fmt.Sprintf("%s:%s@cloudsql-mysql(localhost:%d)/%s?parseTime=true&multiStatements=false&interpolateParams=true&charset=utf8mb4,utf8",
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBPort,
		cfg.DBName,
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	db.SetConnMaxLifetime(10 * time.Minute)
	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(5)

	return db, nil
}
