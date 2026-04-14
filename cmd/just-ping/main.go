package main

import (
	"context"
	"log"

	"partition-maintainer/internal/config"
	"partition-maintainer/internal/db"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadJustPing(ctx)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	database, err := db.OpenMySQLWithConnector(ctx, cfg)
	if err != nil {
		log.Fatalf("open database failed: %v", err)
	}
	defer func() {
		if cerr := database.Close(); cerr != nil {
			log.Printf("close database warning: %v", cerr)
		}
	}()

	if err := database.PingContext(ctx); err != nil {
		log.Fatalf("ping database failed: %v", err)
	}

	log.Printf("database ping succeeded")
}
