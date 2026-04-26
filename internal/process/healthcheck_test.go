package process

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPHealthcheckSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := httpHealthcheck{url: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hc.Check(ctx); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestHTTPHealthcheckNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	hc := httpHealthcheck{url: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hc.Check(ctx); err == nil {
		t.Fatal("expected error on 503, got nil")
	}
}

func TestTCPHealthcheckOpenClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	hc := tcpHealthcheck{address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hc.Check(ctx); err != nil {
		t.Fatalf("expected nil while listening, got %v", err)
	}

	ln.Close()
	if err := hc.Check(ctx); err == nil {
		t.Fatal("expected error after closing listener, got nil")
	}
}

func TestFileHealthcheckPresentAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ready")
	hc := fileHealthcheck{path: path}

	if err := hc.Check(context.Background()); err == nil {
		t.Fatal("expected error when file missing")
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := hc.Check(context.Background()); err != nil {
		t.Fatalf("expected nil after create, got %v", err)
	}
}

func TestCommandHealthcheckExitCodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := (commandHealthcheck{commandLine: "true"}).Check(ctx); err != nil {
		t.Fatalf("true should succeed, got %v", err)
	}
	if err := (commandHealthcheck{commandLine: "false"}).Check(ctx); err == nil {
		t.Fatal("false should fail, got nil")
	}
}

func TestRunOneAttemptAllMustSucceed(t *testing.T) {
	good := commandHealthcheck{commandLine: "true"}
	bad := commandHealthcheck{commandLine: "false"}

	if !runOneAttempt(context.Background(), []healthchecker{good, good}, time.Second) {
		t.Fatal("two passing checks should yield success")
	}
	if runOneAttempt(context.Background(), []healthchecker{good, bad}, time.Second) {
		t.Fatal("any failing check should fail the attempt")
	}
}
