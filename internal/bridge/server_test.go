package bridge

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/ykrsama/katago-remote-one-click/internal/links"
)

const defaultTestToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func startTestServer(t *testing.T) (*Server, *links.Store, string) {
	t.Helper()
	engine := startFakeEngine(t)
	store := links.NewStore(t.TempDir())
	if _, err := store.LoadOrCreate(defaultTestToken); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(engine, store, 1<<20, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return server, store, "ws://" + listener.Addr().String()
}

func dialTestClient(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	connection, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func sendQueryAndReadID(t *testing.T, connection *websocket.Conn, id string) {
	t.Helper()
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":"`+id+`"}`)); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, payload, err := connection.Read(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"id":"`+id+`"`) || strings.Contains(string(payload), "remote_") {
		t.Fatalf("unexpected response for %s: %s", id, payload)
	}
}

func expectClose(t *testing.T, connection *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := connection.Read(readCtx); err != nil {
			if got := websocket.CloseStatus(err); got != want {
				t.Fatalf("close status = %d, want %d (err = %v)", got, want, err)
			}
			return
		}
	}
}

func TestWebSocketServerAuthenticationAndConcurrentClients(t *testing.T) {
	_, _, base := startTestServer(t)
	bad, response, err := websocket.Dial(context.Background(), base+"/katago/wrong", nil)
	if bad != nil {
		_ = bad.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("bad token response = %#v, err = %v", response, err)
	}

	first := dialTestClient(t, base+"/katago/"+defaultTestToken)
	second := dialTestClient(t, base+"/katago/"+defaultTestToken)
	sendQueryAndReadID(t, first, "first-client")
	sendQueryAndReadID(t, second, "second-client")
}

func TestLinkClientLimitRejectsExtraClients(t *testing.T) {
	_, store, base := startTestServer(t)
	limited, err := store.Create(1)
	if err != nil {
		t.Fatal(err)
	}
	first := dialTestClient(t, base+"/katago/"+limited.Token)
	sendQueryAndReadID(t, first, "limited-first")

	second := dialTestClient(t, base+"/katago/"+limited.Token)
	expectClose(t, second, websocket.StatusTryAgainLater)

	// Another link is unaffected.
	other := dialTestClient(t, base+"/katago/"+defaultTestToken)
	sendQueryAndReadID(t, other, "unlimited-other")
}

func TestDisabledLinkRejectsClients(t *testing.T) {
	_, store, base := startTestServer(t)
	disabled, err := store.Create(links.Disabled)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialTestClient(t, base+"/katago/"+disabled.Token)
	expectClose(t, connection, websocket.StatusTryAgainLater)
}

func TestDeletedLinkClosesActiveSessions(t *testing.T) {
	server, store, base := startTestServer(t)
	link, err := store.Create(links.Unlimited)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialTestClient(t, base+"/katago/"+link.Token)
	sendQueryAndReadID(t, connection, "before-delete")

	if _, err := store.Delete(link.ID); err != nil {
		t.Fatal(err)
	}
	server.reconcile()
	expectClose(t, connection, websocket.StatusTryAgainLater)
}

func TestLoweredClientLimitClosesNewestSession(t *testing.T) {
	server, store, base := startTestServer(t)
	link, err := store.Create(links.Unlimited)
	if err != nil {
		t.Fatal(err)
	}
	first := dialTestClient(t, base+"/katago/"+link.Token)
	sendQueryAndReadID(t, first, "older-session")
	second := dialTestClient(t, base+"/katago/"+link.Token)
	sendQueryAndReadID(t, second, "newer-session")

	if _, err := store.SetMaxClients(link.ID, 1); err != nil {
		t.Fatal(err)
	}
	server.reconcile()
	expectClose(t, second, websocket.StatusTryAgainLater)
	sendQueryAndReadID(t, first, "older-session-still-alive")
}

func TestRotatedTokenClosesActiveSessions(t *testing.T) {
	server, store, base := startTestServer(t)
	connection := dialTestClient(t, base+"/katago/"+defaultTestToken)
	sendQueryAndReadID(t, connection, "before-rotation")

	if _, err := store.RotateToken(""); err != nil {
		t.Fatal(err)
	}
	server.reconcile()
	expectClose(t, connection, websocket.StatusTryAgainLater)
}
