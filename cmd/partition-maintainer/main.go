package main

import (
	"context"
	"log"
	"time"

	"partition-maintainer/internal/config"
	"partition-maintainer/internal/db"
	"partition-maintainer/internal/partition"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("load location failed: %v", err)
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

	today := localDayStart(time.Now(), loc)
	log.Printf("partition maintainer start: table=%s today=%s dry_run=%t", cfg.TableName, today.Format("2006-01-02"), cfg.DryRun)

	manager := partition.NewManager(database, cfg)

	unlock, err := manager.AcquireLock(ctx)
	if err != nil {
		log.Fatalf("acquire lock failed: %v", err)
	}
	defer func() {
		if unlock != nil {
			if err := unlock(); err != nil {
				log.Printf("release lock warning: %v", err)
			}
		}
	}()

	if err := manager.Run(ctx, today); err != nil {
		log.Fatalf("partition maintenance failed: %v", err)
	}

	log.Printf("partition maintainer completed")
}

func localDayStart(now time.Time, loc *time.Location) time.Time {
	localNow := now.In(loc)
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
}
