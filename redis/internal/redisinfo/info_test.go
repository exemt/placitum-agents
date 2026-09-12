package redisinfo

import "testing"

func TestParse(t *testing.T) {
	s := Parse(`# Server
redis_version:7.4.2
uptime_in_seconds:123
# Memory
used_memory:1048576
used_memory_rss:2097152
used_memory_peak:3145728
maxmemory:268435456
maxmemory_policy:noeviction
# Clients
connected_clients:3
# Stats
evicted_keys:1
expired_keys:2
keyspace_hits:10
keyspace_misses:4
# Replication
role:master
# Keyspace
db0:keys=2,expires=1,avg_ttl=5000
db1:keys=5,expires=0,avg_ttl=0
`)

	if !s.OK {
		t.Fatal("ok")
	}
	if s.Version != "7.4.2" || s.Role != "master" {
		t.Fatalf("identity: %+v", s)
	}
	if s.UsedMemory != 1048576 || s.MaxMemory != 268435456 {
		t.Fatalf("memory: %+v", s)
	}
	if s.Keys != 7 || s.Expires != 1 {
		t.Fatalf("keys=%d expires=%d", s.Keys, s.Expires)
	}
	if s.Hits != 10 || s.Misses != 4 || s.Clients != 3 {
		t.Fatalf("stats: %+v", s)
	}
}
