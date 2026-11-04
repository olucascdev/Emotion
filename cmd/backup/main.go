package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	mode := flag.String("mode", "backup", "backup or restore")
	databasePath := flag.String("db", "./data/emotion.db", "SQLite database path")
	outputPath := flag.String("out", "./data/backups/emotion.db", "backup or restore target path")
	flag.Parse()
	var err error
	switch *mode {
	case "backup":
		err = backup(*databasePath, *outputPath)
	case "restore":
		err = restore(*outputPath, *databasePath)
	default:
		err = errors.New("mode must be backup or restore")
	}
	if err != nil {
		log.Fatal(err)
	}
}

func backup(source, target string) error {
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("backup target already exists: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	defer db.Close()
	quoted := strings.ReplaceAll(filepath.Clean(target), "'", "''")
	if _, err := db.Exec("VACUUM INTO '" + quoted + "'"); err != nil {
		return fmt.Errorf("create consistent backup: %w", err)
	}
	return nil
}

func restore(source, target string) error {
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("backup source: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".emotion-restore-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	in, err := os.Open(source)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err = io.Copy(tmp, in); err != nil {
		in.Close()
		tmp.Close()
		return err
	}
	in.Close()
	if err = tmp.Close(); err != nil {
		return err
	}
	check, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return err
	}
	var result string
	err = check.QueryRow("PRAGMA integrity_check").Scan(&result)
	check.Close()
	if err != nil || result != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", result)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	return nil
}
