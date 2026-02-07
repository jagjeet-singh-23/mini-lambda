package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jagjeet-singh-23/mini-lambda/shared/logger"
)

// S3Storage handles S3 operations for build artifacts
type S3Storage struct {
	client *s3.Client
	bucket string
}

// NewS3Storage creates a new S3 storage client
func NewS3Storage(endpoint, region, bucket, accessKey, secretKey string) (*S3Storage, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKey,
			secretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})

	return &S3Storage{
		client: client,
		bucket: bucket,
	}, nil
}

// UploadPackage uploads a package to S3
func (s *S3Storage) UploadPackage(ctx context.Context, functionID string, data []byte) (string, error) {
	key := fmt.Sprintf("packages/%s/package.zip", functionID)

	logger.Info("Uploading package to S3", "function_id", functionID, "key", key, "size_bytes", len(data))

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/zip"),
	})

	if err != nil {
		return "", fmt.Errorf("failed to upload package: %w", err)
	}

	logger.Info("Package uploaded successfully", "function_id", functionID, "key", key)
	return key, nil
}

// UploadDirectory uploads a directory to S3 (for extracted packages)
func (s *S3Storage) UploadDirectory(ctx context.Context, functionID, dirPath string) error {
	logger.Info("Uploading directory to S3", "function_id", functionID, "path", dirPath)

	return filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Calculate relative path
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}

		// S3 key
		key := fmt.Sprintf("functions/%s/%s", functionID, relPath)

		// Read file
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		// Upload to S3
		_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader(data),
		})

		if err != nil {
			return fmt.Errorf("failed to upload file %s: %w", key, err)
		}

		logger.Info("Uploaded file", "key", key)
		return nil
	})
}

// DownloadPackage downloads a package from S3
func (s *S3Storage) DownloadPackage(ctx context.Context, key string) ([]byte, error) {
	logger.Info("Downloading package from S3", "key", key)

	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to download package: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read package data: %w", err)
	}

	logger.Info("Package downloaded successfully", "key", key, "size_bytes", len(data))
	return data, nil
}

// DeletePackage deletes a package from S3
func (s *S3Storage) DeletePackage(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("failed to delete package: %w", err)
	}

	logger.Info("Package deleted successfully", "key", key)
	return nil
}
