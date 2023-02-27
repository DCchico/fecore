// Ref: https://gosamples.dev/sqlite-intro/

package storage

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	// "os"
)

type SQLiteStorageManager struct {
	db *sql.DB
}

func NewSQLiteStorageManager(db *sql.DB) (*SQLiteStorageManager, error) {
	query := `
	CREATE TABLE IF NOT EXISTS Function(
		name TEXT PRIMARY KEY UNIQUE,
		namespace TEXT,
		image TEXT,
		labels TEXT,
		annotations TEXT,
		secret TEXT,
		secretsPath TEXT,
		envVars TEXT,
		envProcess TEXT,
		memoryLimit TEXT
	);
	`
	if _, err := db.Exec(query); err != nil {
		return nil, err
	}

	query = `
	CREATE TABLE IF NOT EXISTS Container(
		uuid TEXT PRIMARY KEY UNIQUE,
		parentFunction TEXT
	);
	`
	if _, err := db.Exec(query); err != nil {
		return nil, err
	}
	
	return &SQLiteStorageManager{
		db: db,
	}, nil
}

func (r *SQLiteStorageManager) InsertFunction(function Function) error {
	query := `
	INSERT INTO Function(name, namespace, image, lables, annotations, secret,
	secretsPath, envVars, envProcess, memoryLimit) 
	values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.Exec(query, function.Name, function.Namespace, function.Image, 
		function.Labels, function.Annotations, function.Secret, function.SecretsPath, 
		function.EnvVars, function.EnvProcess, function.MemoryLimit)
	if err != nil {
		return err
	}

	return nil
}

func (r *SQLiteStorageManager) GetAllFunctions() ([]Function, error) {
	rows, err := r.db.Query("SELECT * FROM Function")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fns []Function
	for rows.Next() {
		var f Function
		if err := rows.Scan(&f.Name, &f.Namespace, &f.Image, 
		&f.Labels, &f.Annotations, &f.Secret, &f.SecretsPath, 
		&f.EnvVars, &f.EnvProcess, &f.MemoryLimit); err != nil {
			return nil, err
		}
		fns = append(fns, f)
	}

	return fns, nil
}

func (r *SQLiteStorageManager) DeleteFunction(name string) error {
	_, err := r.db.Exec("DELETE FROM Function WHERE name = ?", name)
	if err != nil {
		return err
	}
	return nil
}

func (r *SQLiteStorageManager) InsertContainer(container Container) error {
	_, err := r.db.Exec("INSERT INTO Container(uuid, parentFunction) values(?, ?)", container.Uuid, container.ParentFunction)
	if err != nil {
		return err
	}

	return nil
}

func (r *SQLiteStorageManager) GetAllContainers() ([]Container, error) {
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

func (r *SQLiteStorageManager) DeleteContainer(uuid string) error {
	_, err := r.db.Exec("DELETE FROM Container WHERE uuid = ?", uuid)
	if err != nil {
		return err
	}
	return nil
}

// func Cleanup(db *sql.DB) {
// 	db.Close()
// 	os.Remove("./func.db")
// }