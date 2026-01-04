package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jagjeet-singh-23/mini-lambda/internal/api"
	"github.com/jagjeet-singh-23/mini-lambda/internal/runtime"
)

func main() {
	log.Println("Mini Lambda starting...")

	runtimeManager, err := runtime.NewManager()
	if err != nil {
		log.Fatalf("Failed to create runtime manager: %v", err)
	}

	log.Printf("Runtime Manager initialized with runtimes: %v", runtimeManager.ListRuntimes())

	handler := api.NewHandler(runtimeManager)

	port := 8080
	if envPort := os.Getenv("PORT"); envPort != "" {
		port, _ = strconv.Atoi(envPort)
	}

	server := api.NewServer(port, handler)

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for Interrupt
	<-shutdownChan

	log.Println("\n Shutdown signal received")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Println("Shutting down HTTP server...")
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Error durign server shutdown: %v", err)
	}

	log.Println("Shutting down runtime manager...")
	if err := runtimeManager.Shutdown(ctx); err != nil {
		log.Printf("Error during runtime shutdown: %v", err)
	}

	log.Println("Shutdown complete.")
}
