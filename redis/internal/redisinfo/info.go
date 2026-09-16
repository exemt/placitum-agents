package redisinfo

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const probeTimeout = 800 * time.Millisecond

type Snapshot struct {
	OK              bool   `json:"ok"`
	Error           string `json:"error,omitempty"`
	Version         string `json:"version,omitempty"`
	Role            string `json:"role,omitempty"`
	UsedMemory      uint64 `json:"used_memory,omitempty"`
	UsedMemoryRSS   uint64 `json:"used_memory_rss,omitempty"`
	UsedMemoryPeak  uint64 `json:"used_memory_peak,omitempty"`
	MaxMemory       uint64 `json:"maxmemory,omitempty"`
	MaxMemoryPolicy string `json:"maxmemory_policy,omitempty"`
	Keys            uint64 `json:"keys,omitempty"`
	Expires         uint64 `json:"expires,omitempty"`
	Evicted         uint64 `json:"evicted,omitempty"`
	Expired         uint64 `json:"expired,omitempty"`
	Hits            uint64 `json:"hits,omitempty"`
	Misses          uint64 `json:"misses,omitempty"`
	Clients         uint64 `json:"clients,omitempty"`
	UptimeS         uint64 `json:"uptime_s,omitempty"`

	CommandsProcessed uint64 `json:"-"`
	NetInputBytes     uint64 `json:"-"`
	NetOutputBytes    uint64 `json:"-"`
	TotalErrors       uint64 `json:"-"`
}

func Collect(parent context.Context, rdb *redis.Client) Snapshot {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return Snapshot{OK: false, Error: err.Error()}
	}

	raw, err := rdb.Info(ctx, "server", "memory", "keyspace", "stats", "clients", "replication").Result()
	if err != nil {
		return Snapshot{OK: true, Error: err.Error()}
	}
	return Parse(raw)
}

func Parse(info string) Snapshot {
	s := Snapshot{OK: true}
	var keys, expires uint64

	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "redis_version":
			s.Version = val
		case "role":
			s.Role = val
		case "used_memory":
			s.UsedMemory = parseU64(val)
		case "used_memory_rss":
			s.UsedMemoryRSS = parseU64(val)
		case "used_memory_peak":
			s.UsedMemoryPeak = parseU64(val)
		case "maxmemory":
			s.MaxMemory = parseU64(val)
		case "maxmemory_policy":
			s.MaxMemoryPolicy = val
		case "evicted_keys":
			s.Evicted = parseU64(val)
		case "expired_keys":
			s.Expired = parseU64(val)
		case "keyspace_hits":
			s.Hits = parseU64(val)
		case "keyspace_misses":
			s.Misses = parseU64(val)
		case "connected_clients":
			s.Clients = parseU64(val)
		case "uptime_in_seconds":
			s.UptimeS = parseU64(val)
		case "total_commands_processed":
			s.CommandsProcessed = parseU64(val)
		case "total_net_input_bytes":
			s.NetInputBytes = parseU64(val)
		case "total_net_output_bytes":
			s.NetOutputBytes = parseU64(val)
		case "total_error_replies":
			s.TotalErrors = parseU64(val)
		default:
			if strings.HasPrefix(key, "db") {
				k, e := parseKeyspace(val)
				keys += k
				expires += e
			}
		}
	}

	s.Keys = keys
	s.Expires = expires
	return s
}

func parseKeyspace(val string) (keys, expires uint64) {
	for _, part := range strings.Split(val, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		n := parseU64(v)
		switch k {
		case "keys":
			keys = n
		case "expires":
			expires = n
		}
	}
	return
}

func parseU64(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
