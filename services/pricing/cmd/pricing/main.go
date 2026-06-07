// Command pricing runs the PricingService gRPC server on :50051 and a
// minimal HTTP health endpoint on :8080.
package main

//go:generate protoc -I../.. --go_out=../.. --go_opt=module=agentbench/services/pricing --go-grpc_out=../.. --go-grpc_opt=module=agentbench/services/pricing proto/v1/pricing.proto

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

	pb "agentbench/services/pricing/api/v1"
	"agentbench/services/pricing/internal/server"

	"google.golang.org/grpc"
)

const (
	grpcAddr = ":50051"
	httpAddr = ":8080"
)

func main() {
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", grpcAddr, err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterPricingServiceServer(grpcServer, server.NewServer())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	httpServer := &http.Server{Addr: httpAddr, Handler: mux}

	go func() {
		log.Printf("gRPC listening on %s", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	go func() {
		log.Printf("HTTP health listening on %s", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down")

	grpcServer.GracefulStop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
}
