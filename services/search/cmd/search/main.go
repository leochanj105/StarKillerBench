// Command search runs the SearchService gRPC server and a health endpoint.
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

	geopb "agentbench/services/geo/api/v1"
	pricingpb "agentbench/services/pricing/api/v1"
	profilepb "agentbench/services/profile/api/v1"
	pb "agentbench/services/search/api/v1"
	"agentbench/services/search/internal/server"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

//go:generate protoc -I ../../proto/v1 --go_out=../../api/v1 --go_opt=paths=source_relative --go-grpc_out=../../api/v1 --go-grpc_opt=paths=source_relative ../../proto/v1/search.proto

func main() {
	geoAddr := getenv("GEO_ADDR", "geo:50051")
	profileAddr := getenv("PROFILE_ADDR", "profile:50051")
	pricingAddr := getenv("PRICING_ADDR", "pricing:50051")

	geoConn := dial(geoAddr)
	defer geoConn.Close()
	profileConn := dial(profileAddr)
	defer profileConn.Close()
	pricingConn := dial(pricingAddr)
	defer pricingConn.Close()

	srv := server.NewServer(
		geopb.NewGeoServiceClient(geoConn),
		profilepb.NewProfileServiceClient(profileConn),
		pricingpb.NewPricingServiceClient(pricingConn),
	)

	grpcServer := grpc.NewServer()
	pb.RegisterSearchServiceServer(grpcServer, srv)

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("listen :50051: %v", err)
	}

	httpServer := &http.Server{Addr: ":8080", Handler: healthMux()}

	go func() {
		log.Printf("gRPC SearchService listening on :50051")
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("grpc serve: %v", err)
		}
	}()
	go func() {
		log.Printf("HTTP health listening on :8080")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop

	log.Printf("shutting down")
	grpcServer.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func healthMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func dial(addr string) *grpc.ClientConn {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial %s: %v", addr, err)
	}
	return conn
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
