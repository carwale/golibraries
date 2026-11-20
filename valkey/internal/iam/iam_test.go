package iam

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func staticCreds(calls *int32) aws.CredentialsProvider {
	return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		atomic.AddInt32(calls, 1)
		return aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}, nil
	})
}

func newTestProvider(serverless bool, calls *int32, now *time.Time) *TokenProvider {
	p := NewTokenProvider("my-iam-user", "my-cache", "ap-south-1", serverless, staticCreds(calls))
	p.now = func() time.Time { return *now }
	return p
}

func TestTokenFormat(t *testing.T) {
	var calls int32
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	p := newTestProvider(false, &calls, &now)

	token, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if strings.HasPrefix(token, "http") {
		t.Fatalf("token must not carry a scheme: %s", token)
	}
	if !strings.HasPrefix(token, "my-cache/?") {
		t.Fatalf("token should start with the cluster name: %s", token)
	}
	q, err := url.ParseQuery(token[strings.Index(token, "?")+1:])
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	want := map[string]string{
		"Action":           "connect",
		"User":             "my-iam-user",
		"X-Amz-Algorithm":  "AWS4-HMAC-SHA256",
		"X-Amz-Expires":    "900",
		"X-Amz-Date":       "20260102T030405Z",
		"X-Amz-Credential": "AKIDEXAMPLE/20260102/ap-south-1/elasticache/aws4_request",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Error("missing X-Amz-Signature")
	}
	if q.Has("ResourceType") {
		t.Error("ResourceType should be set only for serverless caches")
	}

	serverless := newTestProvider(true, &calls, &now)
	token, err = serverless.Token(context.Background())
	if err != nil {
		t.Fatalf("serverless Token: %v", err)
	}
	if !strings.Contains(token, "ResourceType=ServerlessCache") {
		t.Errorf("serverless token missing ResourceType: %s", token)
	}
}

func TestTokenCachedAndRefreshed(t *testing.T) {
	var calls int32
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	p := newTestProvider(false, &calls, &now)

	first, _ := p.Token(context.Background())
	now = now.Add(9 * time.Minute)
	second, _ := p.Token(context.Background())
	if first != second || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("token should be reused within 10 minutes (calls=%d)", calls)
	}

	now = now.Add(2 * time.Minute)
	third, _ := p.Token(context.Background())
	if third == first || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("token should be regenerated after 10 minutes (calls=%d)", calls)
	}

	user, password, err := p.Credentials()
	if err != nil || user != "my-iam-user" || password != third {
		t.Fatalf("Credentials = %q, %q, %v", user, password, err)
	}
}

func TestTokenCredentialError(t *testing.T) {
	p := NewTokenProvider("u", "c", "ap-south-1", false, aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New("no role")
	}))
	if _, err := p.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "no role") {
		t.Fatalf("Token error = %v, want wrapped credential error", err)
	}
}
