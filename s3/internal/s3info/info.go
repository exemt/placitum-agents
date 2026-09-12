package s3info

import (
	"context"
	"time"

	"github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
)

const probeTimeout = 800 * time.Millisecond

// Snapshot — состояние бакета и, если доступен admin API MinIO, объём обменника.
type Snapshot struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Version   string `json:"version,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	Objects   uint64 `json:"objects,omitempty"`
	UsedBytes uint64 `json:"used_bytes,omitempty"`
	Capacity  uint64 `json:"capacity,omitempty"`
	Buckets   uint64 `json:"buckets,omitempty"`
	UptimeS   uint64 `json:"uptime_s,omitempty"`

	// Счётчики канала `api`: растут монотонно, темп — их дельта между
	// опросами (s3info.Rate). Обычный S3 их не отдаёт; есть только у MinIO.
	Requests uint64 `json:"-"`
	InBytes  uint64 `json:"-"`
	OutBytes uint64 `json:"-"`
	Errors   uint64 `json:"-"`
	HasAPI   bool   `json:"-"`
}

type Probe struct {
	S3       *minio.Client
	Admin    *madmin.AdminClient
	Endpoint string
	Secure   bool
	Access   string
	Secret   string
	Bucket   string
	Region   string
}

// Collect опрашивает бакет. Живость — BucketExists. Темп обменника, если MinIO
// отдаёт prometheus-счётчики, — дельта `api`, не латентность самой пробы.
func Collect(parent context.Context, p Probe) Snapshot {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()

	s := Snapshot{
		Endpoint: p.Endpoint,
		Bucket:   p.Bucket,
		Region:   p.Region,
	}

	exists, err := p.S3.BucketExists(ctx, p.Bucket)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	if !exists {
		s.Error = "bucket not found"
		return s
	}
	s.OK = true

	// Сначала запросы: без них шапка карточки снова покажет пустоту, а объём
	// обменника подождёт следующего кадра, если контекст уже на исходе.
	fillAPI(ctx, p, &s)
	if p.Admin != nil {
		fillAdmin(ctx, p.Admin, &s)
	}
	return s
}

func fillAdmin(ctx context.Context, admin *madmin.AdminClient, s *Snapshot) {
	if info, err := admin.ServerInfo(ctx); err == nil {
		var total uint64
		for _, srv := range info.Servers {
			if s.Version == "" && srv.Version != "" {
				s.Version = srv.Version
			}
			if s.UptimeS == 0 && srv.Uptime > 0 {
				s.UptimeS = uint64(srv.Uptime)
			}
			for _, d := range srv.Disks {
				total += d.TotalSpace
			}
		}
		s.Capacity = total
	}

	usage, err := admin.DataUsageInfo(ctx)
	if err != nil {
		return
	}
	s.Objects = usage.ObjectsTotalCount
	s.Buckets = usage.BucketsCount
	s.UsedBytes = usage.ObjectsTotalSize
}
