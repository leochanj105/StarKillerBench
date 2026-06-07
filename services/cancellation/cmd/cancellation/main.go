package main

//go:generate protoc -I ../../proto --go_out=../../api --go_opt=paths=source_relative --go-grpc_out=../../api --go-grpc_opt=paths=source_relative v1/cancellation.proto

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
	pb "agentbench/services/cancellation/api/v1"
	"agentbench/services/cancellation/internal/server"
	inventorypb "agentbench/services/inventory/api/v1"
	paymentpb "agentbench/services/payment/api/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	bookingAddr := envOr("BOOKING_ADDR", "booking:50051")
	paymentAddr := envOr("PAYMENT_ADDR", "payment:50051")
	inventoryAddr := envOr("INVENTORY_ADDR", "inventory:50051")

	bookingConn, err := grpc.NewClient(bookingAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial booking: %v", err)
	}
	defer bookingConn.Close()

	paymentConn, err := grpc.NewClient(paymentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial payment: %v", err)
	}
	defer paymentConn.Close()

	inventoryConn, err := grpc.NewClient(inventoryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial inventory: %v", err)
	}
	defer inventoryConn.Close()

	srv := server.NewServerWithInventory(
		bookingpb.NewBookingServiceClient(bookingConn),
		paymentpb.NewPaymentServiceClient(paymentConn),
		inventorypb.NewInventoryServiceClient(inventoryConn),
	)

	grpcServer := grpc.NewServer()
	pb.RegisterCancellationServiceServer(grpcServer, srv)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpServer := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Printf("gRPC listening on :50051")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()

	go func() {
		log.Printf("HTTP listening on :8080")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	log.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	grpcServer.GracefulStop()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
