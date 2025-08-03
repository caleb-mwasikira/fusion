package database

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	"github.com/caleb-mwasikira/fusion/utils"
	"github.com/go-sql-driver/mysql"
)

var (
	db *sql.DB
)

func init() {
	envFile := filepath.Join(utils.ProjectDir, ".env")
	err := utils.LoadEnvFile(envFile)
	if err != nil {
		log.Fatalf("[ERROR] loading env file; %v\n", err)
	}

	mysqlConfig := mysql.Config{
		User:      os.Getenv("DB_USER"),
		Passwd:    os.Getenv("DB_PASSWORD"),
		DBName:    os.Getenv("DB_NAME"),
		ParseTime: true,
	}
	db, err = openDB(mysqlConfig.FormatDSN())
	if err != nil {
		log.Fatalf("[ERROR] opening database connection; %v", err)
	}
}

func openDB(dsn string) (*sql.DB, error) {
	log.Println("Opening database connection...")

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}
