package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/caleb-mwasikira/fusion/lib"
	_ "github.com/mattn/go-sqlite3"
)

var (
	db *sql.DB
)

func init() {
	var err error
	db, err = openDatabase()
	if err != nil {
		log.Fatalf("Error opening database connection; %v", err)
	}

	err = migrateDatabase()
	if err != nil {
		log.Fatalf("[ERROR] Migration failed; %v\n", err)
	}
}

func openDatabase() (*sql.DB, error) {
	log.Println("Opening database connection...")

	// Create database file if not exists on project directory
	path := filepath.Join(lib.ProjectDir, "fusion.db")

	file, err := os.OpenFile(path, os.O_CREATE, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("error creating sqlite db file; %v", err)
	}
	file.Close()

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

//go:embed sql/*.sql
var sqlDir embed.FS

func migrateDatabase() error {
	files, err := sqlDir.ReadDir("sql")
	if err != nil {
		return err
	}

	if len(files) == 0 {
		log.Println("[WARN] no migration files found")
		return nil
	}

	for _, file := range files {
		log.Printf("[INFO] Migrating file \"%v\" ...\n", file.Name())

		if file.Type().IsRegular() {
			path := fmt.Sprintf("sql/%v", file.Name())

			data, err := sqlDir.ReadFile(path)
			if err != nil {
				return fmt.Errorf("error reading file \"%v\"; %v", file.Name(), err)
			}

			if len(data) == 0 {
				log.Printf("[WARN] sql file \"%v\" empty\n", file.Name())
				continue
			}

			_, err = db.Exec(string(data))
			if err != nil {
				return fmt.Errorf("error executing query from file \"%v\"; %v", file.Name(), err)
			}
			log.Printf("[INFO] Migration \"%v\" successfull\n", file.Name())
		}
	}
	return nil
}
