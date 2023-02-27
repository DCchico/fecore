// Ref: https://gosamples.dev/sqlite-intro/

package storage

import (
	"database/sql"
	// _ "github.com/mattn/go-sqlite3"
	// "os"
)

type SQLiteStorageManager struct {
	db *sql.DB
}

func NewSQLiteStorageManager(db *sql.DB) *SQLiteStorageManager {
	// TODO add table creation code?
	return &SQLiteStorageManager{
		db: db,
	}
}

func (r *SQLiteStorageManager) InsertContainer(container *Container) error {
	_, err := r.db.Exec("INSERT INTO Container(uuid, parentFunction) values(?, ?)", container.Uuid, container.ParentFunction)
	if err != nil {
		return err
	}

	return nil
}

func (r *SQLiteStorageManager) GetAllContainers() ([] Container, error) {
	rows, err := r.db.Query("SELECT * FROM Container")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var containers []Container
	for rows.Next() {
		var c Container
		if err := rows.Scan(&c.Uuid, &c.ParentFunction); err != nil {
			return nil, err
		}
		containers = append(containers, c)
	}

	return containers, nil
}

// TODO add delete and other method code

// func Init() (*sql.DB, error) {
// 	db, err := sql.Open("sqlite3", "./func.db")
// 	if err != nil {
// 		return nil, err
// 	}

// 	query := `
// 	CREATE TABLE IF NOT EXISTS funcs (
// 		id TEXT PRIMARY KEY UNIQUE
// 	);
// 	`
// 	_, err = db.Exec(query)
// 	return db, err
// }

// func Cleanup(db *sql.DB) {
// 	db.Close()
// 	os.Remove("./func.db")
// }