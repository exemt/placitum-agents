package s3info

import "testing"

func TestParseAPICountersV3(t *testing.T) {
	c, ok := parseAPICounters(`
# HELP minio_api_requests_total Total number of API requests
minio_api_requests_total{name="s3.PutObject",type="s3"} 40
minio_api_requests_total{name="s3.GetObject",type="s3"} 60
minio_api_requests_errors_total{name="s3.GetObject",type="s3"} 2
minio_api_requests_traffic_received_bytes{type="s3"} 1000
minio_api_requests_traffic_sent_bytes{type="s3"} 4000
`)
	if !ok {
		t.Fatal("ok")
	}
	if c.Requests != 100 || c.Errs != 2 || c.In != 1000 || c.Out != 4000 {
		t.Fatalf("%+v", c)
	}
}

func TestParseAPICountersV3ErrorsFromStatus(t *testing.T) {
	c, ok := parseAPICounters(`
minio_api_requests_total{name="s3.PutObject"} 10
minio_api_requests_4xx_errors_total{type="s3"} 3
minio_api_requests_5xx_errors_total{type="s3"} 1
`)
	if !ok {
		t.Fatal("ok")
	}
	if c.Requests != 10 || c.Errs != 4 {
		t.Fatalf("%+v", c)
	}
}

func TestParseAPICountersV2(t *testing.T) {
	c, ok := parseAPICounters(`
minio_s3_requests_total{api="PutObject"} 12
minio_s3_requests_total{api="HeadObject"} 8
minio_s3_requests_errors_total{api="PutObject"} 1
minio_s3_traffic_received_bytes 256
minio_s3_traffic_sent_bytes 512
`)
	if !ok {
		t.Fatal("ok")
	}
	if c.Requests != 20 || c.Errs != 1 || c.In != 256 || c.Out != 512 {
		t.Fatalf("%+v", c)
	}
}

func TestParseAPICountersPrefersV3(t *testing.T) {
	c, ok := parseAPICounters(`
minio_api_requests_total{name="s3.PutObject"} 5
minio_s3_requests_total{api="PutObject"} 99
`)
	if !ok {
		t.Fatal("ok")
	}
	if c.Requests != 5 {
		t.Fatalf("got %d", c.Requests)
	}
}

func TestParseAPICountersEmpty(t *testing.T) {
	if _, ok := parseAPICounters("# only comments\n"); ok {
		t.Fatal("expected empty")
	}
}
