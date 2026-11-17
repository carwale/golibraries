// Package iam generates ElastiCache IAM auth tokens.
package iam

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const (
	// tokenValidity is how long ElastiCache accepts a token (fixed by AWS at 15 minutes).
	tokenValidity = 15 * time.Minute
	// refreshAfter is when a cached token is replaced, leaving margin before it expires.
	refreshAfter = 10 * time.Minute
	// credentialTimeout bounds credential retrieval and signing for one connection attempt.
	credentialTimeout = 5 * time.Second
	// emptyPayloadSHA256 is the hex SHA-256 of an empty body, required by SigV4 presigning.
	emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	service            = "elasticache"
)

// TokenProvider generates ElastiCache IAM auth tokens (SigV4-presigned "connect" URLs) and caches
// them. valkey-go asks for credentials on every new connection, so reconnects after a failover or
// the 12-hour ElastiCache connection limit always authenticate with a valid token.
type TokenProvider struct {
	user        string
	clusterName string
	region      string
	serverless  bool
	credentials aws.CredentialsProvider
	signer      *v4.Signer
	now         func() time.Time

	mu       sync.Mutex
	token    string
	issuedAt time.Time
}

// NewTokenProvider returns a provider for user on the given cache. clusterName must already be
// lowercased. serverless adds ResourceType=ServerlessCache.
func NewTokenProvider(user, clusterName, region string, serverless bool, credentials aws.CredentialsProvider) *TokenProvider {
	return &TokenProvider{
		user:        user,
		clusterName: clusterName,
		region:      region,
		serverless:  serverless,
		credentials: credentials,
		signer:      v4.NewSigner(),
		now:         time.Now,
	}
}

// Token returns a cached token, generating a new one when none exists or the cached one is older
// than 10 minutes.
func (p *TokenProvider) Token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if p.token != "" && now.Sub(p.issuedAt) < refreshAfter {
		return p.token, nil
	}
	token, err := p.generate(ctx, now)
	if err != nil {
		return "", err
	}
	p.token, p.issuedAt = token, now
	return token, nil
}

// Credentials returns the user and a valid token, for valkey-go's AuthCredentialsFn.
func (p *TokenProvider) Credentials() (username, password string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
	defer cancel()
	token, err := p.Token(ctx)
	if err != nil {
		return "", "", err
	}
	return p.user, token, nil
}

func (p *TokenProvider) generate(ctx context.Context, signingTime time.Time) (string, error) {
	creds, err := p.credentials.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("valkey: retrieving AWS credentials for IAM auth: %w", err)
	}

	query := url.Values{
		"Action":        {"connect"},
		"User":          {p.user},
		"X-Amz-Expires": {strconv.Itoa(int(tokenValidity.Seconds()))},
	}
	if p.serverless {
		query.Set("ResourceType", "ServerlessCache")
	}
	authURL := url.URL{Scheme: "http", Host: p.clusterName, Path: "/", RawQuery: query.Encode()}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("valkey: building IAM auth request: %w", err)
	}
	signed, _, err := p.signer.PresignHTTP(ctx, creds, req, emptyPayloadSHA256, service, p.region, signingTime)
	if err != nil {
		return "", fmt.Errorf("valkey: presigning IAM auth token: %w", err)
	}
	return strings.TrimPrefix(signed, "http://"), nil
}
