// Package s3client provides a pool for caching and reusing S3 clients.
package s3client

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/example/webdav-s3/ent"
)

// Pool manages cached S3 clients keyed by backend ID.
type Pool struct {
	mu      sync.RWMutex
	clients map[int]*s3.Client
}

// NewPool creates a new client pool.
func NewPool() *Pool {
	return &Pool{
		clients: make(map[int]*s3.Client),
	}
}

// Get returns an S3 client for the given backend, creating it if necessary.
func (p *Pool) Get(ctx context.Context, backend *ent.S3Backend) (*s3.Client, error) {
	p.mu.RLock()
	client, ok := p.clients[backend.ID]
	p.mu.RUnlock()

	if ok {
		return client, nil
	}

	// Create new client
	client, err := p.createClient(ctx, backend)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.clients[backend.ID] = client
	p.mu.Unlock()

	return client, nil
}

// Remove removes a cached client (call when backend is deleted or updated).
func (p *Pool) Remove(backendID int) {
	p.mu.Lock()
	delete(p.clients, backendID)
	p.mu.Unlock()
}

// Clear removes all cached clients.
func (p *Pool) Clear() {
	p.mu.Lock()
	p.clients = make(map[int]*s3.Client)
	p.mu.Unlock()
}

func (p *Pool) createClient(ctx context.Context, backend *ent.S3Backend) (*s3.Client, error) {
	var opts []func(*awsconfig.LoadOptions) error

	opts = append(opts, awsconfig.WithRegion(backend.Region))

	if backend.AccessKey != "" && backend.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				backend.AccessKey,
				backend.SecretKey,
				backend.SessionToken,
			),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if backend.Endpoint != "" {
			o.BaseEndpoint = aws.String(backend.Endpoint)
		}
		o.UsePathStyle = backend.PathStyle
	})

	return client, nil
}

// TestConnection tests if the backend configuration is valid.
func (p *Pool) TestConnection(ctx context.Context, backend *ent.S3Backend) error {
	client, err := p.createClient(ctx, backend)
	if err != nil {
		return err
	}

	// Try to list objects (with max keys = 1) to verify credentials
	_, err = client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(backend.Bucket),
		MaxKeys: aws.Int32(1),
	})
	return err
}