package main

import (
	"os"
	"path/filepath"
	"time"
)

func alive() {
	dir := os.Getenv("WAF_DATA_DIR")
	if dir == "" {
		return
	}

	path := filepath.Join(dir, "alive")
	now := time.Now()

	if err := os.Chtimes(path, now, now); err != nil {
		_ = os.WriteFile(path, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
}
