package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/zeebo/blake3"
)

type S3Config struct {
	Region string

	Bucket string

	Endpoint string

	AccessKeyID     string
	SecretAccessKey string

	SessionToken string

	Prefix string
}

type S3Storage struct {
	client  *s3.Client
	bucket  string
	prefix  string
	isMinIO bool // true if using MinIO (dev), false if using AWS S3 (prod)
}

func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}

	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	var awsCfg aws.Config
	var err error

	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		awsCfg, err = config.LoadDefaultConfig(
			ctx,
			config.WithRegion(cfg.Region),
			config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(
					cfg.AccessKeyID,
					cfg.SecretAccessKey,
					cfg.SessionToken,
				),
			),
		)
	} else {
		awsCfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	var s3Client *s3.Client

	if cfg.Endpoint != "" {
		s3Client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	} else {
		s3Client = s3.NewFromConfig(awsCfg)
	}

	storage := &S3Storage{
		client:  s3Client,
		bucket:  cfg.Bucket,
		prefix:  cfg.Prefix,
		isMinIO: cfg.Endpoint != "", // MinIO uses custom endpoint, AWS S3 doesn't
	}

	if err := storage.ensureBucket(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure bucket: %w", err)
	}

	return storage, nil
}

func (s *S3Storage) ensureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})

	if err == nil {
		return nil
	}

	_, err = s.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.bucket),
	})

	if err != nil {
		return fmt.Errorf("failed to create bucket:%w", err)
	}

	return nil
}

func (s *S3Storage) Store(
	ctx context.Context,
	functionID string,
	code []byte,
) (string, error) {
	if len(code) == 0 {
		return "", fmt.Errorf("code cannot be empty")
	}
	hasher := blake3.New()
	hasher.Write(code)
	hash := hasher.Sum(nil)
	hashStr := hex.EncodeToString(hash)

	key := s.buildKey(hashStr)

	exists, err := s.objectExists(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to check object existence: %w", err)
	}

	if exists {
		return key, nil
	}

	putInput := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(code),
		ContentType:   aws.String("application/octet-stream"),
		ContentLength: aws.Int64(int64(len(code))),

		Metadata: map[string]string{
			"function-id": functionID,
			"blake3":      hashStr,
			"hash-algo":   "blake3",
		},

		StorageClass: types.StorageClassStandard,
	}

	// Only enable server-side encryption for AWS S3 (production)
	// MinIO (dev) doesn't support AES256 encryption without KMS configuration
	if !s.isMinIO {
		putInput.ServerSideEncryption = types.ServerSideEncryptionAes256
	}

	_, err = s.client.PutObject(ctx, putInput)

	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %w", err)
	}

	return key, nil
}

func (s *S3Storage) Retrieve(ctx context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("key cannot be empty")
	}

	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to retreive from S3: %w", err)
	}
	defer result.Body.Close()

	code, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read S3 object: %w", err)
	}

	hashStr := s.extractHashFromKey(key)
	if hashStr != "" {
		hasher := blake3.New()
		hasher.Write(code)
		computedHash := hasher.Sum(nil)
		computedHashStr := hex.EncodeToString(computedHash)

		if computedHashStr != hashStr {
			return nil, fmt.Errorf("code integrity check failed: hash mismatch")
		}
	}

	return code, nil
}

func (s *S3Storage) Delete(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	return nil
}

func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("key cannot be empty")
	}

	return s.objectExists(ctx, key)
}

func (s *S3Storage) objectExists(
	ctx context.Context,
	key string,
) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return false, nil
	}

	return true, nil
}

// buildKey creates an S3 key with prefix and sharding
func (s *S3Storage) buildKey(hash string) string {
	if s.prefix != "" {
		return fmt.Sprintf("%s/%s/%s", s.prefix, hash[:2], hash)
	}
	return fmt.Sprintf("%s/%s", hash[:2], hash)
}

// extractHashFromKey extracts the hash from an S3 key
func (s *S3Storage) extractHashFromKey(key string) string {
	parts := []byte(key)

	lastSlash := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == '/' {
			lastSlash = i
			break
		}
	}

	if lastSlash >= 0 {
		return string(parts[lastSlash+1:])
	}

	return key
}

// GetPresignedURL generates a presigned URl for downloading code
func (s *S3Storage) GetPresignedURL(
	ctx context.Context,
	key string,
	ttl int64,
) (string, error) {
	presignClient := s3.NewPresignClient(s.client)

	presignResult, err := presignClient.PresignGetObject(
		ctx,
		&s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		},
		func(opts *s3.PresignOptions) {
			opts.Expires = time.Duration(ttl) * time.Second
		},
	)

	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignResult.URL, nil
}
