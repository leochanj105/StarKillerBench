// Command auth runs the AuthService gRPC server on :50051 and a /healthz
// endpoint on :8080.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "agentbench/services/auth/api/v1"
	"agentbench/services/auth/internal/server"

	"google.golang.org/grpc"
)

//go:generate protoc --proto_path=../../proto/v1 --go_out=../../api/v1 --go_opt=paths=source_relative --go-grpc_out=../../api/v1 --go-grpc_opt=paths=source_relative auth.proto

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("auth: ")

	grpcServer := grpc.NewServer()
	pb.RegisterAuthServiceServer(grpcServer, server.NewServer())

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen :50051: %v", err)
	}

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	healthSrv := &http.Server{Addr: ":8080", Handler: healthMux}

	go func() {
		log.Printf("gRPC listening on :50051")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()
	go func() {
		log.Printf("health listening on :8080")
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("health serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	log.Printf("shutting down")

	grpcServer.GracefulStop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := healthSrv.Shutdown(ctx); err != nil {
		log.Printf("health shutdown: %v", err)
	}
}
