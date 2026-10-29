package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gtoxlili/mihomo-capture/internal/app"
	"github.com/gtoxlili/mihomo-capture/internal/gateway"
	"github.com/gtoxlili/mihomo-capture/web"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	config := flag.String("config", "config.yaml", "configuration path")
	initIdentity := flag.Bool("init", false, "generate the missing identity files on this machine (CA certificate and key, API and proxy tokens), then exit; existing files are kept")
	check := flag.Bool("check", false, "validate configuration, local identity files, the gateway controller and the capture catalog for the current gateway config, then exit")
	clearGateway := flag.Bool("clear-gateway", false, "empty both capture lanes in the running gateway, then exit; needs no CA or token")
	removeGateway := flag.Bool("remove-gateway", false, "reload the gateway's own config file, which drops the capture lanes, then exit")
	flag.Parse()
	if err := run(*config, *initIdentity, *check, *clearGateway, *removeGateway); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string, initIdentity, check, clearGateway, removeGateway bool) error {
	c, err := app.LoadConfig(path)
	if err != nil {
		return err
	}
	if initIdentity {
		created, err := c.InitIdentity()
		for _, file := range created {
			fmt.Println("created", file)
		}
		return err
	}
	// Operator commands report on the terminal; the service and the wrapper's
	// clear write to the rotated log.
	if c.LogFile != "" && !check && !removeGateway {
		if err = os.MkdirAll(filepath.Dir(c.LogFile), 0700); err != nil {
			return err
		}
		sink := &lumberjack.Logger{Filename: c.LogFile, MaxSize: 10, MaxBackups: 3, MaxAge: 7, Compress: true}
		slog.SetDefault(slog.New(slog.NewJSONHandler(sink, nil)))
	}
	if (c.ControllerSocket == "") == (c.ControllerURL == "") || c.GatewayConfig == "" {
		return errors.New("set exactly one of controller_socket or controller_url, and set gateway_config")
	}
	gate := gateway.NewClient(c.ControllerSocket, c.GatewayConfig, c.GatewayStateDir)

	gate.ControllerToken, err = c.LoadControllerToken()
	if err != nil {
		return err
	}

	if c.ControllerURL != "" {
		if err := gate.UseHTTP(c.ControllerURL, gate.ControllerToken); err != nil {
			return err
		}
	}
	gate.ProxyAddress = c.ProxyListen
	gate.EgressAddress = c.EgressListen
	defer gate.Close()
	if removeGateway {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return gate.Remove(ctx)
	}
	if clearGateway {
		// The wrapper runs this after the service exits.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return gate.Clear(ctx)
	}
	ca, fingerprint, err := c.LoadIdentity()
	if err != nil {
		return err
	}
	gate.Token = c.ProxyToken
	if err = gate.Check(); err != nil {
		return fmt.Errorf("capture catalog for %s: %w", c.GatewayConfig, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = gate.Status(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("check mihomo controller: %w", err)
	}
	if check {
		fmt.Println("configuration valid; CA fingerprint", fingerprint)
		return nil
	}
	a, err := app.New(c, fingerprint, gate)
	if err != nil {
		return err
	}
	proxy, err := app.NewProxy(a, ca)
	if err != nil {
		return err
	}
	apiListener, err := net.Listen("tcp", c.APIListen)
	if err != nil {
		return err
	}
	defer apiListener.Close()
	proxyListener, err := net.Listen("tcp", c.ProxyListen)
	if err != nil {
		return err
	}
	defer proxyListener.Close()
	proxyHost, _, _ := net.SplitHostPort(proxyListener.Addr().String())
	if ip := net.ParseIP(proxyHost); ip == nil || !ip.IsLoopback() {
		return errors.New("proxy_listen must be a loopback IP address")
	}
	// Capture state starts empty; lanes left by an earlier process stop selecting.
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = a.ClearGateway(ctx)
	cancel()
	if err != nil {
		return err
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	err = a.RefreshDevices(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("initial device discovery: %w", err)
	}
	api := &http.Server{Handler: a.Routes(ca.Leaf, web.Handler()), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	data := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 128 << 10}
	serveErrors := make(chan error, 3)
	go func() { serveErrors <- api.Serve(apiListener) }()
	var caServer *http.Server
	if c.CAListen != "" {
		caListener, err := net.Listen("tcp", c.CAListen)
		if err != nil {
			return err
		}
		caServer = &http.Server{Handler: app.CARoutes(ca.Leaf.Raw), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
		go func() { serveErrors <- caServer.Serve(caListener) }()
	}
	go func() { serveErrors <- data.Serve(proxyListener) }()
	slog.Info("service ready", "api", apiListener.Addr(), "proxy", proxyListener.Addr(), "ca", c.CAListen, "ca_fingerprint", fingerprint)
	lifetime, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	neighbors := time.NewTicker(5 * time.Second)
	defer neighbors.Stop()
	prune := time.NewTicker(time.Minute)
	defer prune.Stop()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.StopCapture(ctx)
		_ = a.ClearGateway(ctx)
		proxy.Close()
		_ = data.Close()
		_ = api.Shutdown(ctx)
		if caServer != nil {
			_ = caServer.Close()
		}
	}()
	for {
		select {
		case <-lifetime.Done():
			return nil
		case err := <-serveErrors:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-neighbors.C:
			ctx, cancel := context.WithTimeout(lifetime, 15*time.Second)
			err := a.RefreshDevices(ctx)
			cancel()
			if err != nil {
				slog.Warn("device refresh failed", "error", err)
			}
		case <-prune.C:
			if err := a.Prune(); err != nil {
				slog.Warn("retention cleanup failed", "error", err)
			}
		}
	}
}
