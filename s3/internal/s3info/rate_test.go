package s3info

import (
	"testing"
	"time"
)

func TestRateSample(t *testing.T) {
	var r Rate
	now := time.Unix(1000, 0)
	s1 := Snapshot{Requests: 100, InBytes: 1000, OutBytes: 2000, Errors: 1, HasAPI: true}
	if _, w := r.Sample(s1, now); w != 0 {
		t.Fatal("first sample has no window")
	}

	s2 := Snapshot{Requests: 140, InBytes: 1400, OutBytes: 2800, Errors: 3, HasAPI: true}
	f, w := r.Sample(s2, now.Add(4*time.Second))
	if w != 4 {
		t.Fatalf("window=%d", w)
	}
	if f.Ops != 10 || f.In != 100 || f.Out != 200 || f.Err != 0.5 {
		t.Fatalf("%+v", f)
	}
}

func TestRateSampleReset(t *testing.T) {
	var r Rate
	now := time.Unix(1000, 0)
	r.Sample(Snapshot{Requests: 100, HasAPI: true}, now)
	f, w := r.Sample(Snapshot{Requests: 10, HasAPI: true}, now.Add(time.Second))
	if w != 1 {
		t.Fatalf("window=%d", w)
	}
	if f.Ops != 0 {
		t.Fatalf("reset must be zero, got %v", f.Ops)
	}
}
