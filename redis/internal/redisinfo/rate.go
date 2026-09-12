package redisinfo

import (
	"math"
	"time"

	"github.com/exemt/placitum-agents/redis/internal/flow"
)

// Rate — темп канала `cmd`: дельта счётчиков INFO stats между двумя опросами,
// поделённая на реально прошедшее время. Redis не отдаёт латентность команды
// внешнему наблюдателю, поэтому здесь нет p50/p95/max — только то, что видно
// снаружи: сколько команд и байт прошло за интервал опроса.
type Rate struct {
	at       time.Time
	commands uint64
	in       uint64
	out      uint64
	errs     uint64
}

// Sample сравнивает снимок с предыдущим и возвращает темп и фактическую
// длину интервала в секундах (для window_s — без него дельта на первом или
// запоздавшем опросе не отличима от дельты на штатном).
func (r *Rate) Sample(s Snapshot, now time.Time) (flow.Flow, int) {
	prevAt, prevCommands, prevIn, prevOut, prevErrs := r.at, r.commands, r.in, r.out, r.errs
	r.at, r.commands, r.in, r.out, r.errs = now, s.CommandsProcessed, s.NetInputBytes, s.NetOutputBytes, s.TotalErrors

	if prevAt.IsZero() {
		return flow.Flow{}, 0
	}
	dt := now.Sub(prevAt).Seconds()
	if dt <= 0 {
		return flow.Flow{}, 0
	}

	return flow.Flow{
		Ops: deltaRate(s.CommandsProcessed, prevCommands, dt),
		In:  deltaRate(s.NetInputBytes, prevIn, dt),
		Out: deltaRate(s.NetOutputBytes, prevOut, dt),
		Err: deltaRate(s.TotalErrors, prevErrs, dt),
	}, int(math.Round(dt))
}

// deltaRate — ноль на любой не-рост счётчика: перезапуск Redis (счётчики
// сбросились) не должен превращаться в отрицательный или бесконечный темп.
func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return round1(float64(cur-prev) / dt)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
