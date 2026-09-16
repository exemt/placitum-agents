package s3info

import (
	"math"
	"time"

	"github.com/exemt/placitum-shared/flow"
)

type Rate struct {
	at       time.Time
	requests uint64
	in       uint64
	out      uint64
	errs     uint64
}

func (r *Rate) Sample(s Snapshot, now time.Time) (flow.Flow, int) {
	prevAt, prevReq, prevIn, prevOut, prevErrs := r.at, r.requests, r.in, r.out, r.errs
	r.at, r.requests, r.in, r.out, r.errs = now, s.Requests, s.InBytes, s.OutBytes, s.Errors

	if prevAt.IsZero() {
		return flow.Flow{}, 0
	}
	dt := now.Sub(prevAt).Seconds()
	if dt <= 0 {
		return flow.Flow{}, 0
	}

	return flow.Flow{
		Ops: deltaRate(s.Requests, prevReq, dt),
		In:  deltaRate(s.InBytes, prevIn, dt),
		Out: deltaRate(s.OutBytes, prevOut, dt),
		Err: deltaRate(s.Errors, prevErrs, dt),
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
