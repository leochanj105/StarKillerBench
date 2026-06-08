// Command admin runs the AdminService gRPC server (:50051) and a health
// endpoint (:8080). admin is a stateless proxy that fans writes out to the
// services owning the data (geo, profile, inventory, pricing, ads).
package main

//go:generate protoc --proto_path=../../proto/v1 --go_out=../../api/v1 --go_opt=paths=source_relative --go-grpc_out=../../api/v1 --go-grpc_opt=paths=source_relative admin.proto

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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "agentbench/services/admin/api/v1"
	"agentbench/services/admin/internal/server"
	adspb "agentbench/services/ads/api/v1"
	geopb "agentbench/services/geo/api/v1"
	inventorypb "agentbench/services/inventory/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("admin: %v", err)
	}
}

func run() error {
	geoAddr := env("GEO_ADDR", "geo:50051")
	profileAddr := env("PROFILE_ADDR", "profile:50051")
	inventoryAddr := env("INVENTORY_ADDR", "inventory:50051")
	pricingAddr := env("PRICING_ADDR", "pricing:50051")
	adsAddr := env("ADS_ADDR", "ads:50051")

	geoConn, err := dial(geoAddr)
	if err != nil {
		return err
	}
	defer geoConn.Close()
	profileConn, err := dial(profileAddr)
	if err != nil {
		return err
	}
	defer profileConn.Close()
	inventoryConn, err := dial(inventoryAddr)
	if err != nil {
		return err
	}
	defer inventoryConn.Close()
	pricingConn, err := dial(pricingAddr)
	if err != nil {
		return err
	}
	defer pricingConn.Close()
	adsConn, err := dial(adsAddr)
	if err != nil {
		return err
	}
	defer adsConn.Close()

	srv := server.NewServer(
		geopb.NewGeoServiceClient(geoConn),
		profilepb.NewProfileServiceClient(profileConn),
		inventorypb.NewInventoryServiceClient(inventoryConn),
		pricingpb.NewPricingServiceClient(pricingConn),
		adspb.NewAdsServiceClient(adsConn),
	)

	grpcServer := grpc.NewServer()
	pb.RegisterAdminServiceServer(grpcServer, srv)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		return err
	}

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	httpServer := &http.Server{Addr: ":8080", Handler: healthMux}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("admin: gRPC listening on :50051")
		errCh <- grpcServer.Serve(lis)
	}()
	go func() {
		log.Printf("admin: health listening on :8080")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Printf("admin: received %v, shutting down", sig)
	}

	grpcServer.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(ctx)
}

func dial(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
