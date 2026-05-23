package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	controllerconfig "github.com/akam1o/arca-lb/internal/controller/config"
	"github.com/sirupsen/logrus"
)

func TestDefaultControllerConfigPathLoads(t *testing.T) {
	path := filepath.Join("..", "..", *configPath)
	cfg, err := controllerconfig.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(%q): %v", *configPath, err)
	}
	if cfg.DataStore.Type == "" {
		t.Fatal("default controller config did not set datastore.type")
	}
}

func TestMySQLDatastoreRegistered(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	addr := listener.Addr().(*net.TCPAddr)
	_, err = datastore.NewDataStore(context.Background(), &datastore.Config{
		Type:          "mysql",
		MySQLHost:     "127.0.0.1",
		MySQLPort:     addr.Port,
		MySQLUser:     "arcalb",
		MySQLPassword: "arcalbpass",
		MySQLDatabase: "arcalb",
	})
	if err == nil {
		t.Fatal("expected mysql connection error")
	}
	if strings.Contains(err.Error(), "unsupported datastore type") {
		t.Fatalf("mysql datastore is not registered: %v", err)
	}
}

type fakeControllerServer struct {
	startErr   error
	started    chan struct{}
	stopped    chan struct{}
	stopCalled chan struct{}
	stopOnce   sync.Once
}

func newFakeControllerServer(startErr error) *fakeControllerServer {
	return &fakeControllerServer{
		startErr:   startErr,
		started:    make(chan struct{}),
		stopped:    make(chan struct{}),
		stopCalled: make(chan struct{}),
	}
}

func (s *fakeControllerServer) Start() error {
	close(s.started)
	if s.startErr != nil {
		return s.startErr
	}
	<-s.stopped
	return nil
}

func (s *fakeControllerServer) Shutdown(context.Context) error {
	s.stop()
	return nil
}

func (s *fakeControllerServer) Stop(context.Context) error {
	s.stop()
	return nil
}

func (s *fakeControllerServer) stop() {
	s.stopOnce.Do(func() {
		close(s.stopCalled)
		close(s.stopped)
	})
}

func TestRunControllerServersStopsBothServersOnStartError(t *testing.T) {
	apiErr := errors.New("listen failed")
	apiServer := newFakeControllerServer(apiErr)
	grpcServer := newFakeControllerServer(nil)
	sigChan := make(chan os.Signal, 1)
	logger := discardLogrus()

	codeCh := make(chan int, 1)
	go func() {
		codeCh <- runControllerServers(apiServer, grpcServer, sigChan, logger)
	}()

	<-apiServer.started
	code := waitForExitCode(t, codeCh)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	waitForStop(t, apiServer)
	waitForStop(t, grpcServer)
}

func TestRunControllerServersStopsBothServersOnSignal(t *testing.T) {
	apiServer := newFakeControllerServer(nil)
	grpcServer := newFakeControllerServer(nil)
	sigChan := make(chan os.Signal, 1)
	logger := discardLogrus()

	codeCh := make(chan int, 1)
	go func() {
		codeCh <- runControllerServers(apiServer, grpcServer, sigChan, logger)
	}()

	<-apiServer.started
	<-grpcServer.started
	sigChan <- syscall.SIGTERM

	code := waitForExitCode(t, codeCh)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	waitForStop(t, apiServer)
	waitForStop(t, grpcServer)
}

func discardLogrus() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logger
}

func waitForExitCode(t *testing.T, codeCh <-chan int) int {
	t.Helper()
	select {
	case code := <-codeCh:
		return code
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for controller run to exit")
		return 0
	}
}

func waitForStop(t *testing.T, server *fakeControllerServer) {
	t.Helper()
	select {
	case <-server.stopCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server stop")
	}
}
