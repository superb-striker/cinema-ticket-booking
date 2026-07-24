package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/superb-striker/cinema/internal/database"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	directory := os.Getenv("MIGRATIONS_DIR")
	if directory == "" {
		directory = "migrations"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db, directory); err != nil {
		log.Fatal(err)
	}
	log.Println("database migrations are up to date")
}
