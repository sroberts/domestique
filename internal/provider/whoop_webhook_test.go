package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"testing"
	"time"
)

func sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write(body)
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestVerifyWhoopSignature(t *testing.T) {
	now := time.Now()
	body := []byte(`{"user_id":10129,"id":"ecfc6a15-4661-442f-a9a4-f160dd7afae8","type":"workout.updated","trace_id":"t1"}`)
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	good := sign("secret", ts, body)
	old := strconv.FormatInt(now.Add(-3*time.Hour).UnixMilli(), 10)
	for _, c := range []struct {
		name        string
		secret, sig string
		ts          string
		body        []byte
		ok          bool
	}{
		{"genuine", "secret", good, ts, body, true},
		{"wrong secret", "other", good, ts, body, false},
		{"tampered body", "secret", good, ts, append([]byte{}, append(body[:len(body)-1], ' ', '}')...), false},
		{"timestamp swapped", "secret", good, strconv.FormatInt(now.UnixMilli()+1, 10), body, false},
		{"too old, even if signed", "secret", sign("secret", old, body), old, body, false},
		{"not base64", "secret", "%%%", ts, body, false},
		{"missing", "secret", "", ts, body, false},
		{"no secret configured", "", sign("", ts, body), ts, body, false},
	} {
		err := VerifyWhoopSignature(c.secret, c.sig, c.ts, c.body, now)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}
