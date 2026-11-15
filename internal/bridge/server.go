package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/ykrsama/katago-remote-one-click/internal/links"
)

const linkReconcileInterval = 2 * time.Second

type Server struct {
	engine          *Engine
	links           *links.Store
	maxMessageBytes int64
	logger          *log.Logger
	httpServer      *http.Server

	sessionsMu sync.Mutex
	sessions   map[string]*clientSession
}

type clientSession struct {
	id         string
	linkID     string
	token      string
	startedAt  time.Time
	connection *websocket.Conn
}

func NewServer(engine *Engine, store *links.Store, maxMessageBytes int64, logger *log.Logger) (*Server, error) {
	if engine == nil {
		return nil, errors.New("engine is required")
	}
	if store == nil {
		return nil, errors.New("link store is required")
	}
	if maxMessageBytes <= 0 {
		maxMessageBytes = 4 << 20
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	server := &Server{
		engine:          engine,
		links:           store,
		maxMessageBytes: maxMessageBytes,
		logger:          logger,
		sessions:        make(map[string]*clientSession),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.health)
	mux.HandleFunc("/katago/", server.websocket)
	server.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	return server, nil
}

func (s *Server) Serve(listener net.Listener) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.reconcileLoop(ctx)
	s.logger.Printf("WebSocket bridge listening on %s", listener.Addr())
	err := s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.sessionsMu.Lock()
	sessions := make([]*clientSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.sessionsMu.Unlock()
	for _, session := range sessions {
		_ = session.connection.Close(websocket.StatusGoingAway, "server shutting down")
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	provided := strings.TrimPrefix(r.URL.Path, "/katago/")
	if provided == "" || strings.Contains(provided, "/") {
		http.NotFound(w, r)
		return
	}
	registry, err := s.links.LoadOrCreate("")
	if err != nil {
		s.logger.Printf("load private links failed: %v", err)
		http.Error(w, "private link registry unavailable", http.StatusInternalServerError)
		return
	}
	link, ok := links.FindByToken(registry, provided)
	if !ok {
		http.NotFound(w, r)
		return
	}

	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		// Browser clients are often served from another origin (for example a
		// GitHub Pages build). Origin checks protect cookie-based authentication;
		// here the private path token is the only credential, so any origin is
		// allowed to connect.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.Printf("WebSocket handshake failed: %v", err)
		return
	}
	connection.SetReadLimit(s.maxMessageBytes)
	session := &clientSession{
		id:         newSessionID(),
		linkID:     link.ID,
		token:      provided,
		startedAt:  time.Now(),
		connection: connection,
	}
	if !s.admit(session, link) {
		s.logger.Printf("rejected client for link %s (max clients: %s)", link.ID, links.DescribeMax(link.MaxClients))
		_ = connection.Close(websocket.StatusTryAgainLater, "link is disabled or at its client limit")
		return
	}
	sender := &websocketSender{
		id:         session.id,
		connection: connection,
	}
	s.logger.Printf("client connected (session=%s, link=%s)", session.id, link.ID)
	defer func() {
		s.removeSession(session.id)
		s.engine.CancelSession(session.id)
		_ = connection.Close(websocket.StatusNormalClosure, "session ended")
		s.logger.Printf("client disconnected (session=%s, link=%s)", session.id, link.ID)
	}()

	for {
		messageType, payload, err := connection.Read(r.Context())
		if err != nil {
			status := websocket.CloseStatus(err)
			if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
				s.logger.Printf("client read ended (session=%s, status=%d): %v", session.id, status, err)
			}
			return
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		for _, line := range strings.Split(string(payload), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if err := s.engine.Submit(r.Context(), sender, []byte(line)); err != nil {
				s.logger.Printf("client request failed (session=%s): %v", session.id, err)
				return
			}
		}
	}
}

func (s *Server) admit(session *clientSession, link links.Link) bool {
	if link.MaxClients == links.Disabled {
		return false
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	if link.MaxClients != links.Unlimited {
		active := 0
		for _, existing := range s.sessions {
			if existing.linkID == link.ID {
				active++
			}
		}
		if active >= link.MaxClients {
			return false
		}
	}
	s.sessions[session.id] = session
	return true
}

func (s *Server) removeSession(id string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	delete(s.sessions, id)
}

func (s *Server) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(linkReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcile()
		}
	}
}

// reconcile applies link edits made on disk: deleted links, rotated tokens,
// disabled links, and client-limit changes close the affected sessions.
func (s *Server) reconcile() {
	registry, err := s.links.LoadOrCreate("")
	if err != nil {
		s.logger.Printf("reload private links failed: %v", err)
		return
	}
	byID := make(map[string]links.Link, len(registry))
	for _, link := range registry {
		byID[link.ID] = link
	}

	s.sessionsMu.Lock()
	victims := make([]*clientSession, 0)
	perLink := make(map[string][]*clientSession)
	for _, session := range s.sessions {
		link, exists := byID[session.linkID]
		if !exists || link.Token != session.token || link.MaxClients == links.Disabled {
			victims = append(victims, session)
			continue
		}
		if link.MaxClients != links.Unlimited {
			perLink[link.ID] = append(perLink[link.ID], session)
		}
	}
	for linkID, sessions := range perLink {
		limit := byID[linkID].MaxClients
		if len(sessions) <= limit {
			continue
		}
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].startedAt.Before(sessions[j].startedAt)
		})
		victims = append(victims, sessions[limit:]...)
	}
	for _, session := range victims {
		delete(s.sessions, session.id)
	}
	s.sessionsMu.Unlock()

	for _, session := range victims {
		s.logger.Printf("closing session=%s (link=%s): link removed, rotated, disabled, or over its limit", session.id, session.linkID)
		_ = session.connection.Close(websocket.StatusTryAgainLater, "link disabled, revoked, or over its client limit")
	}
}

func newSessionID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value)
}

type websocketSender struct {
	id         string
	connection *websocket.Conn
	writeMu    sync.Mutex
}

func (s *websocketSender) SessionID() string {
	return s.id
}

func (s *websocketSender) Send(ctx context.Context, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.connection.Write(ctx, websocket.MessageText, payload)
}

func ProtocolError(id, message string) []byte {
	payload, _ := json.Marshal(map[string]string{"id": id, "error": message})
	return payload
}
