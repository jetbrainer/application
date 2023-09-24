package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Option interface {
	Apply(service *Service) error
}

type SubService interface {
	Ready() bool
	Name() string
	Close() error
}

type DB interface {
	Ping(ctx context.Context) error
	Close() error
}

type Redis interface {
	Ping(ctx context.Context) (string, error)
	Close() error
}

type GRPCServer struct {
	address string
	server  *grpc.Server
}

type Service struct {
	Name        string
	ctx         context.Context
	GRPCServers []*GRPCServer
	HTTPServers []*http.Server
	DB          DB
	Redis       Redis
	isReady     *atomic.Value
	ErrChan     chan error
	SubServices map[string]SubService
	sigHandler  SignalTrap
}

func New(ctx context.Context, name string, options ...Option) (*Service, error) {
	isReady := &atomic.Value{}
	isReady.Store(false)

	s := &Service{
		Name:        name,
		ErrChan:     make(chan error),
		ctx:         ctx,
		isReady:     isReady,
		SubServices: make(map[string]SubService),
	}

	for _, o := range options {
		if err := o.Apply(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (s *Service) GetContext() context.Context {
	return s.ctx
}

func (s *Service) SetContext(ctx context.Context) {
	s.ctx = ctx
}

func (s *Service) AddHTTPServer(httpServer *http.Server) {
	s.HTTPServers = append(s.HTTPServers, httpServer)
}

func (s *Service) AddGRPCService(serverName string, service interface{}, description *grpc.ServiceDesc) error {
	for _, grpcServer := range s.GRPCServers {
		if grpcServer.address == serverName {
			grpcServer.server.RegisterService(description, service)
			log.Debug().Msgf("GRPC service registered. service - %s, server - %s", description.ServiceName, serverName)
			return nil
		}
	}
	return errors.New("gRPC server not found")
}

func (s *Service) IsAlive() bool {
	isGrpcAlive := true
	if s.GRPCServers != nil {
		isGrpcAlive = s.checkGRPCServerUp()
		if !isGrpcAlive {
			log.Debug().Msg("grpc servers not ready")
		}
	}

	areHTTPServersAlive := true
	for _, httpServer := range s.HTTPServers {
		if !s.checkHTTPServerUp(httpServer) {
			areHTTPServersAlive = false
		}
	}

	isDBAlive := true
	if s.DB != nil && !s.checkDBAlive() {
		isDBAlive = false
	}

	isRedisAlive := true
	if s.Redis != nil && !s.checkRedisAlive() {
		isRedisAlive = false
	}

	return isGrpcAlive && areHTTPServersAlive && isDBAlive && isRedisAlive // add redis
}

func (s *Service) Start() error {
	ctx := s.GetContext()

	for _, httpServ := range s.HTTPServers {
		httpServ := httpServ
		go func() {
			log.Info().Msgf("started http server address", httpServ.Addr)
			defer log.Info().Msg("stopped http server")

			if err := httpServ.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.ErrChan <- fmt.Errorf("http: failed to serve %v", err)
			}
		}()
	}

	for _, grpcServer := range s.GRPCServers {
		grpcServer := grpcServer

		go func() {
			log.Info().Msgf("started grpc server address", grpcServer.address)
			defer log.Info().Msg("stopped grpc server")

			listener, err := net.Listen("tcp", grpcServer.address)
			if err != nil {
				s.ErrChan <- fmt.Errorf("failed to listenn %v", err)
				return
			}

			if err = grpcServer.server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				s.ErrChan <- fmt.Errorf("grpc: failed to serve %v", err)
			}
		}()
	}

	go s.Ready()

	{
		if err := s.sigHandler.Wait(ctx); err != nil && !errors.Is(err, ErrTermSig) {
			log.Error().Msgf("failed to caught signal", log.Err(err))
			return err
		}
		log.Info().Msg("termination signal received")
	}

	return nil
}

func (s *Service) Stop() {
	for _, service := range s.SubServices {
		if err := service.Close(); err != nil {
			log.Error().Msgf("failed to stop service service %s", service.Name())
		}

		log.Debug().Msgf("subservice stopped subservice %s", service.Name())
	}

	for _, grpcServer := range s.GRPCServers {
		grpcServer.server.GracefulStop()
		log.Debug().Msg("grpc server stopped")
	}

	for _, httpServer := range s.HTTPServers {
		if err := httpServer.Shutdown(s.ctx); err != nil {
			log.Error().Msgf("failed to shutdown http server %s", httpServer.Addr)
		}
		log.Debug().Msg("http server stopped")
	}

	if s.DB != nil {
		if err := s.DB.Close(); err != nil {
			log.Error().Msg("failed to close connection to db")
		}

		log.Debug().Msg("db stopped")
	}

	if s.Redis != nil {
		if err := s.Redis.Close(); err != nil {
			log.Error().Msg("failed to close connection to redis")
		}

		log.Debug().Msg("redis stopped")
	}

	os.Exit(1)
}

func (s *Service) Ready() {
	areSubServicesReady := true
	for _, subService := range s.SubServices {
		if !subService.Ready() {
			log.Error().Msgf("subservice not ready subservice %s", subService.Name())
			areSubServicesReady = false
		}
		log.Info().Msgf("subservice is ready subservice %s", subService.Name())
	}

	isGRPCReady := true
	if s.GRPCServers != nil {
		isGRPCReady = s.checkGRPCServerUp()
		if !isGRPCReady {
			log.Error().Msg("grpc server not ready")
		}
	}

	areHTTPServersReady := true
	for _, httpServer := range s.HTTPServers {
		if !s.checkHTTPServerUp(httpServer) {
			areHTTPServersReady = false
		}
	}

	isDBReady := true
	if s.DB != nil && !s.checkDBAlive() {
		isDBReady = false
	}

	isRedisAlive := true
	if s.Redis != nil && !s.checkRedisAlive() {
		isRedisAlive = false
	}

	s.isReady.Swap(areSubServicesReady && isGRPCReady && areHTTPServersReady && isDBReady && isRedisAlive)
}
func (s *Service) checkHTTPServerUp(httpServer *http.Server) bool {
	err := errors.New("http server not ready")
	var conn net.Conn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	for err != nil {
		if conn, err = net.DialTimeout("tcp", httpServer.Addr, 1*time.Second); err != nil {
			log.Debug().Msg(err.Error())
		}
	}
	log.Debug().Msg("http server ready")
	return true
}

func (s *Service) checkGRPCServerUp() bool {
	ctx := s.GetContext()
	var conn *grpc.ClientConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()

	for _, server := range s.GRPCServers {
		var err error
		// WithBlock will block dial until the server is ready
		if conn, err = grpc.DialContext(ctx, server.address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock()); err != nil {
			log.Debug().Msg(err.Error())
			return false
		}

		log.Debug().Msgf("grpc server ready %s", server.address)
	}
	return true
}

func (s *Service) checkDBAlive() bool {
	err := s.DB.Ping(s.ctx)
	if err != nil {
		log.Debug().Msgf("db is not ready", log.Err(err))
	}
	isReady := err == nil
	if isReady {
		log.Debug().Msg("db is ready")
	}

	return isReady
}

func (s *Service) checkRedisAlive() bool {
	pong, err := s.Redis.Ping(s.ctx)
	if err != nil {
		log.Debug().Msgf("redis is not ready", log.Err(err))
	}
	isReady := pong == "PONG"
	if isReady {
		log.Debug().Msg("redis is ready")
	}

	return isReady
}
