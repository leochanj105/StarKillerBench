// Command ads runs the AdsService gRPC server on :50051 and a health
// endpoint on :8080.
package main

//go:generate protoc -I ../.. --go_out=../.. --go_opt=module=agentbench/services/ads --go-grpc_out=../.. --go-grpc_opt=module=agentbench/services/ads proto/v1/ads.proto

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "agentbench/services/ads/api/v1"
	"agentbench/services/ads/internal/server"

	"google.golang.org/grpc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	// gRPC server.
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen :50051: %v", err)
	}
	grpcSrv := grpc.NewServer()
	pb.RegisterAdsServiceServer(grpcSrv, server.NewServer())

	go func() {
		log.Printf("ads gRPC listening on :50051")
		if err := grpcSrv.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	// HTTP health server.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Printf("ads health listening on :8080")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http serve: %v", err)
		}
	}()

	// Graceful shutdown on SIGTERM/SIGINT.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	log.Printf("shutting down")

	grpcSrv.GracefulStop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
}
