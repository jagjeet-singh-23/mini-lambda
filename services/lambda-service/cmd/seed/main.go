package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/services/lambda-service/internal/storage"
	_ "github.com/lib/pq"
)

func main() {
	ctx := context.Background()

	s3, err := storage.NewS3Storage(ctx, storage.S3Config{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
		Bucket:          "lambda-functions",
	})
	if err != nil {
		log.Fatalf("S3 init failed: %v", err)
	}

	functionID := "seed-fn-001"
	code := []byte(`async function handler(event, context) {
  return { statusCode: 200, body: JSON.stringify({ message: "hello from mini-lambda", input: event }) };
}`)

	codeKey, err := s3.Store(ctx, functionID, code)
	if err != nil {
		log.Fatalf("S3 store failed: %v", err)
	}
	log.Printf("Code stored at S3 key: %s", codeKey)

	db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=mini_lambda sslmode=disable")
	if err != nil {
		log.Fatalf("DB open failed: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("DB ping failed: %v", err)
	}

	now := time.Now()
	_, err = db.ExecContext(ctx, `
		INSERT INTO functions (id, name, runtime, handler, code_key, timeout_seconds, memory_mb, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			code_key = EXCLUDED.code_key,
			updated_at = CURRENT_TIMESTAMP
	`, functionID, "seed-function", "nodejs18", "index.handler", codeKey, 30, 128, now, now)
	if err != nil {
		log.Fatalf("DB insert failed: %v", err)
	}

	fmt.Printf("\nFunction seeded successfully!\n")
	fmt.Printf("  ID:       %s\n", functionID)
	fmt.Printf("  Code key: %s\n", codeKey)
	fmt.Printf("\nInvoke with:\n")
	fmt.Printf(`  curl -s -X POST http://localhost:8081/functions/%s/invoke -H 'Content-Type: application/json' -d '{"name":"world"}' | jq .`+"\n", functionID)
}
