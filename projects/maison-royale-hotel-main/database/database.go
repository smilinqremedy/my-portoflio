package database

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func Connect() *sql.DB {
	db, err := sql.Open("sqlite", "hotel.db")
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}

	if err := db.Ping(); err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	createBookingsTable(db)
	addGuestColumns(db)

	log.Println("Database connected successfully")

	return db
}

func createBookingsTable(db *sql.DB) {
	query := `
	CREATE TABLE IF NOT EXISTS bookings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		check_in TEXT NOT NULL,
		check_out TEXT NOT NULL,
		guests INTEGER NOT NULL,
		room_id INTEGER NOT NULL,
		guest_name TEXT NOT NULL DEFAULT '',
		guest_email TEXT NOT NULL DEFAULT '',
		guest_phone TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := db.Exec(query)
	if err != nil {
		log.Fatal("Failed to create bookings table:", err)
	}

	log.Println("Bookings table ready")
}

// addGuestColumns brings databases created before guest details existed up to
// date. CREATE TABLE IF NOT EXISTS leaves an existing table alone, so the new
// columns have to be added explicitly.
func addGuestColumns(db *sql.DB) {
	guestColumns := []string{
		"guest_name",
		"guest_email",
		"guest_phone",
	}

	for _, column := range guestColumns {
		exists, err := columnExists(db, "bookings", column)
		if err != nil {
			log.Fatal("Failed to inspect bookings table:", err)
		}

		if exists {
			continue
		}

		// Column names come from the fixed list above, not from user input.
		// SQLite does not accept placeholders for identifiers.
		statement := fmt.Sprintf(
			"ALTER TABLE bookings ADD COLUMN %s TEXT NOT NULL DEFAULT ''",
			column,
		)

		if _, err := db.Exec(statement); err != nil {
			log.Fatalf("Failed to add %s column: %v", column, err)
		}

		log.Println("Added missing column:", column)
	}
}

func columnExists(db *sql.DB, table string, column string) (bool, error) {
	rows, err := db.Query(
		fmt.Sprintf("PRAGMA table_info(%s)", table),
	)

	if err != nil {
		return false, err
	}

	defer rows.Close()

	for rows.Next() {
		var (
			id           int
			name         string
			dataType     string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)

		err := rows.Scan(
			&id,
			&name,
			&dataType,
			&notNull,
			&defaultValue,
			&primaryKey,
		)

		if err != nil {
			return false, err
		}

		if name == column {
			return true, nil
		}
	}

	return false, rows.Err()
}
