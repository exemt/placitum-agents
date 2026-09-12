package s3info

import "testing"

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in     string
		host   string
		secure bool
	}{
		{"http://minio:9000", "minio:9000", false},
		{"https://s3.example.com", "s3.example.com", true},
		{"minio:9000", "minio:9000", false},
		{"http://127.0.0.1:9002/", "127.0.0.1:9002", false},
	}
	for _, c := range cases {
		got, err := ParseEndpoint(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got.Host != c.host || got.Secure != c.secure {
			t.Fatalf("%s: %+v", c.in, got)
		}
	}
}

func TestParseEndpointEmpty(t *testing.T) {
	if _, err := ParseEndpoint(""); err == nil {
		t.Fatal("expected error")
	}
}
