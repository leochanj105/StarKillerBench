package main

//go:generate protoc -I ../../proto --go_out=../.. --go_opt=module=agentbench/services/booking --go-grpc_out=../.. --go-grpc_opt=module=agentbench/services/booking ../../proto/v1/booking.proto

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

	bookingpb "agentbench/services/booking/api/v1"
	"agentbench/services/booking/internal/server"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	paymentAddr := envOr("PAYMENT_ADDR", "payment:50051")
	inventoryAddr := envOr("INVENTORY_ADDR", "inventory:50051")

	payConn, err := grpc.NewClient(paymentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial payment: %v", err)
	}
	defer payConn.Close()

	invConn, err := grpc.NewClient(inventoryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial inventory: %v", err)
	}
	defer invConn.Close()

	srv := server.NewServer(
		paymentpb.NewPaymentServiceClient(payConn),
		inventorypb.NewInventoryServiceClient(invConn),
	)

	grpcServer := grpc.NewServer()
	bookingpb.RegisterBookingServiceServer(grpcServer, srv)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpServer := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Printf("grpc listening on :50051")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()
	go func() {
		log.Printf("http listening on :8080")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	grpcServer.GracefulStop()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
