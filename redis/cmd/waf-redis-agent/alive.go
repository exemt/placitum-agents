package main

import (
	"os"
	"path/filepath"
	"time"
)

/*
 * Отметка жизни для healthcheck контейнера: порта у агента нет, а TCP-пинг
 * шины ничего не сказал бы о нём самом. Файл обновляется после каждого
 * отправленного кадра присутствия, проверка образа смотрит на его возраст.
 * Отказ записи -- не повод падать: контроллер видит кадры и без него.
 */
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
