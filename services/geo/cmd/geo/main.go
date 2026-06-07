// Command geo runs the GeoService gRPC server and a health endpoint.
package main

//go:generate protoc -I ../../proto/v1 --go_out=../.. --go_opt=module=agentbench/services/geo --go-grpc_out=../.. --go-grpc_opt=module=agentbench/services/geo ../../proto/v1/geo.proto

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "agentbench/services/geo/api/v1"
	"agentbench/services/geo/internal/server"

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
	pb.RegisterGeoServiceServer(grpcServer, server.NewServer())

	go func() {
		log.Printf("geo gRPC listening on %s", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpServer := &http.Server{Addr: httpAddr, Handler: mux}

	go func() {
		log.Printf("geo HTTP /healthz listening on %s", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	log.Print("geo shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	grpcServer.GracefulStop()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
}
