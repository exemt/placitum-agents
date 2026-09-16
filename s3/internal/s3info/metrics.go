package s3info

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const metricsLimit = 1 << 20

type apiCounters struct {
	Requests uint64
	In       uint64
	Out      uint64
	Errs     uint64
}

func fillAPI(ctx context.Context, p Probe, s *Snapshot) {
	if p.Access == "" || p.Secret == "" || p.Endpoint == "" {
		return
	}
	body, err := scrapeMetrics(ctx, p)
	if err != nil {
		return
	}
	c, ok := parseAPICounters(string(body))
	if !ok {
		return
	}
	s.Requests = c.Requests
	s.InBytes = c.In
	s.OutBytes = c.Out
	s.Errors = c.Errs
	s.HasAPI = true
}

func scrapeMetrics(ctx context.Context, p Probe) ([]byte, error) {
	token, err := prometheusJWT(p.Access, p.Secret)
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if p.Secure {
		scheme = "https"
	}
	base := scheme + "://" + p.Endpoint
	var last error
	for _, path := range []string{
		"/minio/metrics/v3/api/requests",
		"/minio/v2/metrics/cluster",
	} {
		body, err := getMetrics(ctx, base+path, token)
		if err == nil {
			return body, nil
		}
		last = err
	}
	if last == nil {
		return nil, fmt.Errorf("metrics empty")
	}
	return nil, last
}

func getMetrics(ctx context.Context, rawURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("metrics %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, metricsLimit))
}

func prometheusJWT(access, secret string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS512","typ":"JWT"}`))
	payload, err := json.Marshal(struct {
		Iss string `json:"iss"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}{
		Iss: "prometheus",
		Sub: access,
		Exp: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

type promFamily struct {
	req, in, out, errs, e4, e5 uint64
	seen                       bool
}

func parseAPICounters(text string) (apiCounters, bool) {
	var v3, v2 promFamily
	for _, line := range strings.Split(text, "\n") {
		name, val, ok := parsePromLine(line)
		if !ok {
			continue
		}
		switch name {
		case "minio_api_requests_total":
			v3.req += val
			v3.seen = true
		case "minio_api_requests_errors_total":
			v3.errs += val
			v3.seen = true
		case "minio_api_requests_4xx_errors_total":
			v3.e4 += val
			v3.seen = true
		case "minio_api_requests_5xx_errors_total":
			v3.e5 += val
			v3.seen = true
		case "minio_api_requests_traffic_received_bytes":
			v3.in += val
			v3.seen = true
		case "minio_api_requests_traffic_sent_bytes":
			v3.out += val
			v3.seen = true
		case "minio_s3_requests_total":
			v2.req += val
			v2.seen = true
		case "minio_s3_requests_errors_total":
			v2.errs += val
			v2.seen = true
		case "minio_s3_traffic_received_bytes":
			v2.in += val
			v2.seen = true
		case "minio_s3_traffic_sent_bytes":
			v2.out += val
			v2.seen = true
		}
	}
	pick := v2
	if v3.seen {
		pick = v3
		if pick.errs == 0 {
			pick.errs = pick.e4 + pick.e5
		}
	}
	if !pick.seen {
		return apiCounters{}, false
	}
	return apiCounters{
		Requests: pick.req,
		In:       pick.in,
		Out:      pick.out,
		Errs:     pick.errs,
	}, true
}

func parsePromLine(line string) (name string, value uint64, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", 0, false
	}
	var rest string
	if i := strings.IndexByte(line, '{'); i >= 0 {
		name = line[:i]
		j := strings.LastIndexByte(line, '}')
		if j <= i {
			return "", 0, false
		}
		rest = strings.TrimSpace(line[j+1:])
	} else {
		var found bool
		name, rest, found = strings.Cut(line, " ")
		if !found {
			return "", 0, false
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", 0, false
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || f < 0 {
		return "", 0, false
	}
	return name, uint64(f), true
}
