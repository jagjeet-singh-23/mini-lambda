package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/api"
	"github.com/jagjeet-singh-23/mini-lambda/internal/config"
	"github.com/jagjeet-singh-23/mini-lambda/internal/domain"
	"github.com/jagjeet-singh-23/mini-lambda/internal/runtime"
	"github.com/jagjeet-singh-23/mini-lambda/internal/storage"
)

const warmupContainers = 5

func main() {
	instanceID := os.Getenv("INSTANCE_ID")
	if instanceID == "" {
		instanceID = "local"
	}

	log.Printf("Mini Lambda starting... (Instance: %s)", instanceID)

	cfg := loadConfig()
	ctx := context.Background()

	postgresRepo := initPostgres(cfg)
	defer postgresRepo.Close()

	codeStorage := initS3(ctx, cfg)
	functionService := domain.NewFunctionService(postgresRepo, codeStorage)

	runtimeManager := initRuntimeManager()
	warmUpPools(ctx, runtimeManager)

	server := setupServer(cfg, runtimeManager, functionService)

	handleGracefulShutdown(server, runtimeManager)
}

func loadConfig() *config.Config {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	return cfg
}

func initPostgres(cfg *config.Config) *storage.PostgresRepository {
	repo, err := storage.NewPostgresRepository(storage.PostgresConfig{
		Host:     cfg.Postgres.Host,
		Port:     cfg.Postgres.Port,
		User:     cfg.Postgres.User,
		Password: cfg.Postgres.Password,
		Database: cfg.Postgres.Database,
		SSLMode:  cfg.Postgres.SSLMode,
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("PostgreSQL connected successfully.")
	return repo
}

func initS3(ctx context.Context, cfg *config.Config) domain.CodeStorage {
	s3Storage, err := storage.NewS3Storage(ctx, storage.S3Config{
		Region:          cfg.S3.Region,
		Bucket:          cfg.S3.Bucket,
		Endpoint:        cfg.S3.Endpoint,
		AccessKeyID:     cfg.S3.AccessKeyID,
		SecretAccessKey: cfg.S3.SecretAccessKey,
		Prefix:          cfg.S3.Prefix,
	})
	if err != nil {
		log.Fatalf("Failed to create S3 storage: %v", err)
	}
	log.Println("S3 storage connected successfully.")
	return s3Storage
}

func initRuntimeManager() *runtime.Manager {
	manager, err := runtime.NewManager()
	if err != nil {
		log.Fatalf("Failed to create runtime manager: %v", err)
	}
	log.Printf("Runtime Manager initialized with runtimes: %v", manager.ListRuntimes())
	return manager
}

func warmUpPools(ctx context.Context, manager *runtime.Manager) {
	runtimes := []string{"python3.9", "nodejs18"}
	for _, rt := range runtimes {
		rtObj, _ := manager.GetRuntime(rt)
		for range warmupContainers {
			rtObj.(*runtime.DockerRuntime).Pool.CreateNew(ctx)
		}
	}
}

func setupServer(cfg *config.Config, rm *runtime.Manager, fs *domain.FunctionService) *api.Server {
	handler := api.NewHandler(rm, fs)
	return api.NewServer(cfg.Server.Port, handler)
}

func handleGracefulShutdown(server *api.Server, rm *runtime.Manager) {
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-shutdownChan
	log.Println("\nShutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}

	if err := rm.Shutdown(ctx); err != nil {
		log.Printf("Error during runtime shutdown: %v", err)
	}
	log.Println("Shutdown complete.")
}
