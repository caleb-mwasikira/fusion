package db

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/go-sql-driver/mysql"
	// _ "github.com/mattn/go-sqlite3"
)

var (
	SECRET_KEY string

	db *sql.DB
)

type databaseType uint

const (
	mysqlDb databaseType = iota
	sqliteDb
)

func init() {
	err := lib.LoadEnv()
	if err != nil {
		log.Fatalf("Error loading env variables; %v\n", err)
	}

	// Ensure SECRET_KEY is always set
	SECRET_KEY = os.Getenv("SECRET_KEY")

	if strings.TrimSpace(SECRET_KEY) == "" {
		log.Fatalln("Missing SECRET_KEY env variable")
	}

	mysqlConfig := mysql.Config{
		User:                 os.Getenv("DB_USER"),
		Passwd:               os.Getenv("DB_PASSWORD"),
		DBName:               os.Getenv("DB_NAME"),
		ParseTime:            true,
		AllowNativePasswords: true,
	}
	db, err = openMysqlDB(mysqlConfig)
	if err != nil {
		log.Fatalf("Error opening MySQL database connection; %v", err)
	}

	// err = migrateDatabase(mysqlDb)
	// if err != nil {
	// 	log.Fatalf("Migration failed; %v\n", err)
	// }
}

func openMysqlDB(conf mysql.Config) (*sql.DB, error) {
	addr := conf.Addr
	if addr == "" {
		addr = "localhost"
	}

	log.Printf("'%v'@'%v' connecting to MySQL database...\n", conf.User, addr)

	db, err := sql.Open("mysql", conf.FormatDSN())
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// func openSqlite3DB() (*sql.DB, error) {
// 	log.Println("Opening database connection...")

// 	// Create database file if not exists on project directory
// 	path := filepath.Join(lib.ProjectDir, "fusion.db")

// 	file, err := os.OpenFile(path, os.O_CREATE, 0755)
// 	if err != nil && !errors.Is(err, os.ErrExist) {
// 		return nil, fmt.Errorf("error creating sqlite db file; %v", err)
// 	}
// 	file.Close()

// 	db, err := sql.Open("sqlite3", path)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if err = db.Ping(); err != nil {
// 		return nil, err
// 	}
// 	return db, nil
// }

//go:embed sql/*.sql
var sqlDir embed.FS

func migrateDatabase(dbType databaseType) error {
	files, err := sqlDir.ReadDir("sql")
	if err != nil {
		return err
	}

	if len(files) == 0 {
		log.Println("[WARN] no migration files found")
		return nil
	}

	switch dbType {
	case mysqlDb:
		files = slices.DeleteFunc(files, func(file os.DirEntry) bool {
			isMysqlFile := strings.Contains(file.Name(), "mysql")
			return !isMysqlFile
		})
	case sqliteDb:
		files = slices.DeleteFunc(files, func(file os.DirEntry) bool {
			isSqliteFile := strings.Contains(file.Name(), "sqlite")
			return !isSqliteFile
		})
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

			dangerousKeywords := []string{"DROP", "TRUNCATE"}
			isDangerousQuery := func(query string) bool {
				upper := strings.ToUpper(query)

				for _, keyword := range dangerousKeywords {
					if strings.Contains(upper, keyword) {
						// dangerous query
						return true
					}
				}
				return false
			}

			careful := true
			skipAllDangerous := false

			queries := strings.Split(string(data), ";")

			for _, query := range queries {
				query = strings.TrimSpace(query)
				if query == "" {
					continue
				}

				dangerousQuery := isDangerousQuery(query)
				if dangerousQuery && skipAllDangerous {
					continue
				}

				permissionRequired := dangerousQuery && careful
				if permissionRequired {
					// Request permission to execute
					log.Printf("⚠️ Detected potentially dangerous query:\n%v\n", query)
					log.Println("Do you want to still execute it? Y/n/S (Skip all)/A (Yes to all)")

					var input string
					if _, err = fmt.Scanln(&input); err != nil {
						return err
					}

					switch strings.ToUpper(strings.TrimSpace(input)) {
					case "Y":
						// allow this query only
					case "A":
						// allow all future dangerous queries
						careful = false
					case "S":
						// skip this query and all future dangerous queries
						skipAllDangerous = true
						continue
					default:
						return fmt.Errorf("user stopped execution; exiting")
					}
				}

				log.Printf("Executing query; %s\n", query)
				_, err = db.Exec(query)
				if err != nil {
					return fmt.Errorf("error executing query %v; %v", query, err)
				}
			}

			log.Printf("[INFO] Migration \"%v\" successfull\n", file.Name())
		}
	}
	return nil
}
