package redisinfo

import (
	"math"
	"time"

	"github.com/exemt/placitum-shared/flow"
)

type Rate struct {
	at       time.Time
	commands uint64
	in       uint64
	out      uint64
	errs     uint64
}

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

func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return round1(float64(cur-prev) / dt)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
