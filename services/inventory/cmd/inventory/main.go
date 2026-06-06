package main

//go:generate protoc -I ../../proto --go_out=../.. --go_opt=module=agentbench/services/inventory --go-grpc_out=../.. --go-grpc_opt=module=agentbench/services/inventory ../../proto/v1/inventory.proto

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "agentbench/services/inventory/api/v1"
	"agentbench/services/inventory/internal/server"
	"agentbench/services/inventory/internal/store"

	"google.golang.org/grpc"
)

func main() {
	// Default backend is Postgres (PG_DSN); fall back to the in-memory
	// store for local dev when PG_DSN is unset.
	srv := server.NewServer()
	if dsn := os.Getenv("PG_DSN"); dsn != "" {
		pg, err := store.NewPGStore(context.Background(), dsn)
		if err != nil {
			log.Fatalf("connect Postgres: %v", err)
		}
		defer pg.Close()
		srv = server.NewServerWithStore(pg)
		log.Printf("using Postgres backend")
	} else {
		log.Printf("PG_DSN unset; using in-memory backend")
	}

	grpcLis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen :50051: %v", err)
	}

	grpcSrv := grpc.NewServer()
	pb.RegisterInventoryServiceServer(grpcSrv, srv)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("gRPC listening on :50051")
		errCh <- grpcSrv.Serve(grpcLis)
	}()
	go func() {
		log.Printf("HTTP listening on :8080")
		err := httpSrv.ListenAndServe()
		if err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		log.Printf("server error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	grpcSrv.GracefulStop()
	log.Printf("shutdown complete")
}
