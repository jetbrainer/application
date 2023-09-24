package main

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	s "github.com/jetbrainer/application"
)

func main() {
	ctx := context.Background()

	service, err := s.New(
		ctx,
		"test",
		s.WithGRPCServer(":8080"),
		s.WithTechHTTPServerOption(":5051"),
	)
	if err != nil {
		return
	}
	defer service.Stop()

	{
		service.AddHTTPServer(&http.Server{
			Addr:           ":8081",
			Handler:        chi.NewRouter(),
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: http.DefaultMaxHeaderBytes,
		})
	}

	if err = service.Start(); err != nil {
		return
	}
}
