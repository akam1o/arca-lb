package main

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/akam1o/arca-lb/internal/common/datastore"
)

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
