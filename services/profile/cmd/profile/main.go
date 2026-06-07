// Command profile runs the ProfileService gRPC server on :50051 and a health
// endpoint on :8080.
package main

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

	pb "agentbench/services/profile/api/v1"
	"agentbench/services/profile/internal/server"

	"google.golang.org/grpc"
)

//go:generate protoc --proto_path=../../proto/v1 --go_out=../../api/v1 --go_opt=paths=source_relative --go-grpc_out=../../api/v1 --go-grpc_opt=paths=source_relative profile.proto

func main() {
	grpcSrv := grpc.NewServer()
	pb.RegisterProfileServiceServer(grpcSrv, server.NewServer())

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen :50051: %v", err)
	}

	go func() {
		log.Printf("profile gRPC listening on :50051")
		if err := grpcSrv.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpSrv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Printf("profile health listening on :8080")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	grpcSrv.GracefulStop()
}
