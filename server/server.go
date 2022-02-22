package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	pb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip support.
	"google.golang.org/grpc/reflection"

	"github.com/znly/bazel-cache/cache"
	_ "github.com/znly/bazel-cache/cache/disk"
	_ "github.com/znly/bazel-cache/cache/gcs"
)

type cacheServer struct {
	cache *CacheEx
}

var serveCmdFlags = struct {
	listenAddr string
	cacheURI   string
}{}

func getDefaultListen() string {
	var sb strings.Builder
	sb.WriteString("0.0.0.0:")
	sb.WriteString(viper.GetString("PORT"))
	return sb.String()
}

func init() {
	viper.AutomaticEnv()

	ServeCmd.Flags().StringVarP(
		&serveCmdFlags.listenAddr,
		"listen_addr",
		"a",
		getDefaultListen(),
		"listen address",
	)
	ServeCmd.Flags().StringVarP(
		&serveCmdFlags.cacheURI,
		"cache",
		"c",
		viper.GetString("CACHE_URI"),
		"cache uri",
	)
}

func newHTTPandGRPCMux(
	http1Hand http.Handler,
	http2Hand http.Handler,
	grpcHandler http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("content-type"), "application/grpc") {
			grpcHandler.ServeHTTP(w, r)
			return
		}

		http2Hand.ServeHTTP(w, r)
		return
	})
}

var ServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Starts the Bazel cache gRPC server",
	RunE: func(cmd *cobra.Command, args []string) error {
		listenAddr := serveCmdFlags.listenAddr

		cc, err := cache.NewCacheFromURI(context.Background(), serveCmdFlags.cacheURI)

		if err != nil {
			return err
		}

		cs := &cacheServer{
			cache: NewCacheEx(cc),
		}

		grpcServer := grpc.NewServer(
			grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
				grpc_zap.StreamServerInterceptor(zap.L()),
			)),
			grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
				grpc_zap.UnaryServerInterceptor(zap.L()),
			)),
			grpc.ReadBufferSize(maxChunkSize),
			grpc.WriteBufferSize(maxChunkSize),
		)
		pb.RegisterActionCacheServer(grpcServer, cs)
		pb.RegisterCapabilitiesServer(grpcServer, cs)
		pb.RegisterContentAddressableStorageServer(grpcServer, cs)
		bytestream.RegisterByteStreamServer(grpcServer, cs)
		reflection.Register(grpcServer)

		http1Mux := http.NewServeMux()
		http1Mux.HandleFunc("/", home)
		http2Mux := http.NewServeMux()
		mixedHandler := newHTTPandGRPCMux(
			http1Mux,
			http2Mux,
			grpcServer,
		)
		http2Server := &http2.Server{}
		http1Server := &http.Server{Handler: h2c.NewHandler(mixedHandler, http2Server)}

		lis, err := net.Listen("tcp", listenAddr)

		if err != nil {
			panic(err)
		}

		if errors.Is(err, http.ErrServerClosed) {
			zap.L().Error("Server closed")
		} else if err != nil {
			panic(err)
		}

		zap.L().With(
			zap.String("addr", lis.Addr().String()),
			zap.String("cache", serveCmdFlags.cacheURI),
		).Info("Listening")

		return http1Server.Serve(lis)
	},
}

func home(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello from http handler!\n")
}
