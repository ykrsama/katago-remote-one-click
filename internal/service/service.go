package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ykrsama/katago-remote-one-click/internal/bridge"
	appconfig "github.com/ykrsama/katago-remote-one-click/internal/config"
	"github.com/ykrsama/katago-remote-one-click/internal/links"
)

type InitOptions struct {
	KataGo         string
	Model          string
	HumanModel     string
	Config         string
	Cloudflared    string
	Listen         string
	MaxVisits      int64
	TunnelMode     string
	TunnelHostname string
	TunnelToken    string
	TunnelProtocol string
}

type Status struct {
	Running    bool
	PID        int
	Connection *ConnectionState
}

type Check struct {
	Name    string
	OK      bool
	Message string
}

func Initialize(home string, options InitOptions) error {
	cfg := appconfig.Default()
	if existing, err := appconfig.Load(home); err == nil {
		cfg = existing
	}
	paths := []struct {
		name  string
		value string
		dest  *string
	}{
		{"KataGo", options.KataGo, &cfg.KataGoPath},
		{"model", options.Model, &cfg.ModelPath},
		{"analysis config", options.Config, &cfg.AnalysisConfig},
		{"cloudflared", options.Cloudflared, &cfg.CloudflaredPath},
	}
	for _, item := range paths {
		if item.value == "" {
			continue
		}
		absolute, err := filepath.Abs(item.value)
		if err != nil {
			return fmt.Errorf("resolve %s path: %w", item.name, err)
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			return fmt.Errorf("%s was not found at %s", item.name, absolute)
		}
		*item.dest = absolute
	}
	for _, item := range paths {
		if *item.dest == "" {
			return fmt.Errorf("%s path is required", item.name)
		}
	}
	if options.HumanModel != "" {
		absolute, err := filepath.Abs(options.HumanModel)
		if err != nil {
			return fmt.Errorf("resolve human model path: %w", err)
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			return fmt.Errorf("human model was not found at %s", absolute)
		}
		cfg.HumanModelPath = absolute
	}
	if options.Listen != "" {
		cfg.Listen = options.Listen
	}
	if options.MaxVisits > 0 {
		cfg.MaxVisits = options.MaxVisits
	}
	if options.TunnelMode != "" || options.TunnelHostname != "" {
		mode := options.TunnelMode
		if mode == "" {
			mode = appconfig.TunnelModeNamed
		}
		normalized, err := appconfig.NormalizeTunnelMode(mode)
		if err != nil {
			return err
		}
		cfg.TunnelMode = normalized
	}
	if options.TunnelHostname != "" {
		cfg.TunnelHostname = strings.ToLower(strings.TrimSpace(options.TunnelHostname))
	}
	if options.TunnelProtocol != "" {
		protocol, err := appconfig.NormalizeTunnelProtocol(options.TunnelProtocol)
		if err != nil {
			return err
		}
		cfg.TunnelProtocol = protocol
	}
	if cfg.NamedTunnel() {
		if cfg.TunnelHostname == "" {
			return errors.New("a tunnel hostname is required for a named tunnel")
		}
		token := strings.TrimSpace(options.TunnelToken)
		if token != "" {
			if err := appconfig.SaveTunnelToken(home, token); err != nil {
				return err
			}
		} else if !appconfig.HasTunnelToken(home) {
			return errors.New("a Cloudflare tunnel token is required for a named tunnel")
		}
	} else {
		cfg.TunnelHostname = ""
	}
	if err := appconfig.Save(home, cfg); err != nil {
		return err
	}
	_, err := appconfig.EnsureToken(home, false)
	return err
}

func Run(ctx context.Context, home, version string, logger *log.Logger) error {
	if err := appconfig.EnsurePrivateDirs(home); err != nil {
		return err
	}
	cfg, err := appconfig.Load(home)
	if err != nil {
		return err
	}
	// The access token is only a migration source for the link registry now.
	legacyToken, _ := appconfig.ReadToken(home)
	store := links.NewStore(home)
	registry, err := store.LoadOrCreate(legacyToken)
	if err != nil {
		return err
	}
	tunnelToken := ""
	if cfg.NamedTunnel() {
		tunnelToken, err = appconfig.ReadTunnelToken(home)
		if err != nil {
			return err
		}
	}
	if err := writeCurrentProcessState(home); err != nil {
		return fmt.Errorf("record service process: %w", err)
	}
	defer removeCurrentProcessState(home)
	defer os.Remove(connectionStatePath(home))

	engine, err := bridge.Start(ctx, bridge.EngineConfig{
		Executable:      cfg.KataGoPath,
		Model:           cfg.ModelPath,
		HumanModel:      cfg.HumanModelPath,
		Config:          cfg.AnalysisConfig,
		WorkingDir:      home,
		Environment:     cfg.ExtraEnvironment,
		MaxVisits:       cfg.MaxVisits,
		StartupTimeout:  cfg.StartupTimeout,
		ShutdownTimeout: 8 * time.Second,
	}, logger)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		_ = engine.Shutdown(context.Background())
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	server, err := bridge.NewServer(engine, store, cfg.MaxMessageBytes, logger)
	if err != nil {
		_ = listener.Close()
		_ = engine.Shutdown(context.Background())
		return err
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Serve(listener)
		close(serverDone)
	}()

	protocol := cfg.TunnelProtocol
	if configured := strings.TrimSpace(os.Getenv("TUNNEL_TRANSPORT_PROTOCOL")); configured != "" {
		// An explicit environment variable still wins, matching cloudflared.
		protocol = configured
	}
	if protocol == appconfig.TunnelProtocolAuto {
		protocol = ""
	}
	tunnelProcess, baseURL, err := startTunnel(ctx, tunnelSpec{
		Executable:   cfg.CloudflaredPath,
		LocalAddress: cfg.Listen,
		Timeout:      cfg.TunnelTimeout,
		Named:        cfg.NamedTunnel(),
		Hostname:     cfg.TunnelHostname,
		Token:        tunnelToken,
		Protocol:     protocol,
	}, logger)
	if err != nil {
		shutdown(ctx, server, engine, nil)
		return err
	}
	connectionURL := ""
	if len(registry) > 0 {
		connectionURL, err = buildConnectionURL(baseURL, registry[0].Token)
		if err != nil {
			shutdown(ctx, server, engine, tunnelProcess)
			return err
		}
	}
	state := ConnectionState{
		URL:       connectionURL,
		BaseURL:   baseURL,
		CreatedAt: time.Now().UTC(),
		Version:   version,
	}
	if err := writePrivateJSON(connectionStatePath(home), state); err != nil {
		shutdown(ctx, server, engine, tunnelProcess)
		return fmt.Errorf("save connection link: %w", err)
	}
	if connectionURL != "" {
		if _, err := WriteQR(home, connectionURL); err != nil {
			logger.Printf("QR image could not be written: %v", err)
		}
	}
	logger.Printf("secure tunnel ready at %s (access token omitted)", baseURL)
	if cfg.NamedTunnel() {
		logger.Printf("named tunnel: the Cloudflare Public Hostname %s must target http://%s", cfg.TunnelHostname, cfg.Listen)
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-engine.Done():
		if runErr == nil {
			runErr = errors.New("KataGo exited unexpectedly")
		} else {
			runErr = fmt.Errorf("KataGo exited unexpectedly: %w", runErr)
		}
	case runErr = <-serverDone:
		if runErr == nil {
			runErr = errors.New("WebSocket server stopped unexpectedly")
		}
	case runErr = <-tunnelProcess.done:
		if runErr == nil {
			runErr = errors.New("secure tunnel stopped unexpectedly")
		} else {
			runErr = fmt.Errorf("secure tunnel stopped unexpectedly: %w", runErr)
		}
	}
	shutdown(context.Background(), server, engine, tunnelProcess)
	return runErr
}

func shutdown(ctx context.Context, server *bridge.Server, engine *bridge.Engine, tunnelProcess *tunnel) {
	if tunnelProcess != nil {
		tunnelProcess.stop(5 * time.Second)
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if server != nil {
		_ = server.Shutdown(shutdownCtx)
	}
	if engine != nil {
		_ = engine.Shutdown(shutdownCtx)
	}
}

func buildConnectionURL(baseURL, token string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse tunnel URL: %w", err)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported tunnel URL scheme %q", parsed.Scheme)
	}
	parsed.Path = "/katago/" + token
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func StartDaemon(home, version string, output io.Writer) error {
	cfg, err := appconfig.Load(home)
	if err != nil {
		return err
	}
	if _, err := linkStore(home); err != nil {
		return err
	}
	if cfg.NamedTunnel() {
		if _, err := appconfig.ReadTunnelToken(home); err != nil {
			return err
		}
	}
	status, _ := ReadStatus(home)
	if status.Running {
		return Show(home, output)
	}
	if err := appconfig.EnsurePrivateDirs(home); err != nil {
		return err
	}
	_ = os.Remove(connectionStatePath(home))
	_ = os.Remove(processStatePath(home))
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(home, appconfig.LogFileName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open service log: %w", err)
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "run", "--home", home, "--version", version)
	cmd.Dir = home
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(cfg.StartupTimeout + cfg.TunnelTimeout + 15*time.Second)
	for time.Now().Before(deadline) {
		status, _ := ReadStatus(home)
		if status.Running && status.Connection != nil {
			return Show(home, output)
		}
		if state, err := readProcessState(home); err == nil && !processMatches(state) {
			return fmt.Errorf("service stopped during startup; inspect %s", logPath)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("service startup timed out; inspect %s", logPath)
}

func Stop(home string, output io.Writer) error {
	state, err := readProcessState(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(output, "Already stopped / 服务未运行")
			return nil
		}
		return err
	}
	if !processMatches(state) {
		_ = os.Remove(processStatePath(home))
		_ = os.Remove(connectionStatePath(home))
		_, _ = fmt.Fprintln(output, "Already stopped / 服务未运行")
		return nil
	}
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop service process %s: %w", pidDescription(state), err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !processMatches(state) {
			_ = os.Remove(processStatePath(home))
			_ = os.Remove(connectionStatePath(home))
			_, _ = fmt.Fprintln(output, "Stopped / 已停止")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if processMatches(state) {
		_ = process.Kill()
	}
	_ = os.Remove(processStatePath(home))
	_ = os.Remove(connectionStatePath(home))
	_, _ = fmt.Fprintln(output, "Stopped after forced cleanup / 已强制停止")
	return nil
}

func ReadStatus(home string) (Status, error) {
	state, err := readProcessState(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Status{}, nil
		}
		return Status{}, err
	}
	status := Status{PID: state.PID, Running: processMatches(state)}
	if !status.Running {
		return status, nil
	}
	data, err := os.ReadFile(connectionStatePath(home))
	if err == nil {
		var connection ConnectionState
		if json.Unmarshal(data, &connection) == nil && connection.BaseURL != "" {
			status.Connection = &connection
		}
	}
	return status, nil
}

func Show(home string, output io.Writer) error {
	status, err := ReadStatus(home)
	if err != nil {
		return err
	}
	if !status.Running {
		return errors.New("service is not running; run katago-remote start")
	}
	if status.Connection == nil {
		return errors.New("service is still starting; try again shortly")
	}
	store, err := linkStore(home)
	if err != nil {
		return err
	}
	registry, err := store.Load()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(output, "")
	_, _ = fmt.Fprintln(output, "Ready / 远程算力已就绪")
	if len(registry) == 0 {
		_, _ = fmt.Fprintln(output, "")
		_, _ = fmt.Fprintln(output, "No private links are configured. Run `katago-remote link create`.")
		_, _ = fmt.Fprintln(output, "尚未创建私密链接。运行 `katago-remote link create`。")
		return nil
	}
	for index, link := range registry {
		value, err := buildConnectionURL(status.Connection.BaseURL, link.Token)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "\n[%s] max clients: %s\n%s\n", link.ID, links.DescribeMax(link.MaxClients), value)
		if index == 0 {
			_ = PrintQR(output, value)
			_, _ = fmt.Fprintf(output, "\nQR image / 二维码图片: %s\n", filepath.Join(home, "state", "connection-qr.png"))
		}
	}
	_, _ = fmt.Fprintln(output, "Keep these links private. Paste one into LizzieYzy Next > Remote Compute > Custom Compute.")
	_, _ = fmt.Fprintln(output, "请勿公开这些链接。将其中一个粘贴到 LizzieYzy Next > 远程算力 > 自建算力。")
	return nil
}

func ResetLink(home, version string, output io.Writer) error {
	if err := Stop(home, io.Discard); err != nil {
		return err
	}
	store, err := linkStore(home)
	if err != nil {
		return err
	}
	rotated, err := store.RotateToken("")
	if err != nil {
		return err
	}
	if err := appconfig.SaveToken(home, rotated.Token); err != nil {
		return err
	}
	return StartDaemon(home, version, output)
}

func linkStore(home string) (*links.Store, error) {
	store := links.NewStore(home)
	legacy := ""
	if token, err := appconfig.ReadToken(home); err == nil {
		legacy = token
	}
	if _, err := store.LoadOrCreate(legacy); err != nil {
		return nil, err
	}
	return store, nil
}

func connectionBaseURL(home string) string {
	data, err := os.ReadFile(connectionStatePath(home))
	if err != nil {
		return ""
	}
	var state ConnectionState
	if json.Unmarshal(data, &state) != nil {
		return ""
	}
	return state.BaseURL
}

func linkURL(home string, link links.Link) (string, bool) {
	base := connectionBaseURL(home)
	if base == "" {
		return "", false
	}
	value, err := buildConnectionURL(base, link.Token)
	if err != nil {
		return "", false
	}
	return value, true
}

func printLink(home string, link links.Link, output io.Writer) {
	if value, ok := linkURL(home, link); ok {
		_, _ = fmt.Fprintln(output, value)
		return
	}
	_, _ = fmt.Fprintf(output, "token: %s\n", link.Token)
	_, _ = fmt.Fprintln(output, "(start the service to get the full wss URL)")
}

func CreateLink(home string, maxClients int, output io.Writer) error {
	store, err := linkStore(home)
	if err != nil {
		return err
	}
	created, err := store.Create(maxClients)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Created link %s (max clients: %s)\n", created.ID, links.DescribeMax(created.MaxClients))
	printLink(home, created, output)
	return nil
}

func ListLinks(home string, output io.Writer) error {
	store, err := linkStore(home)
	if err != nil {
		return err
	}
	registry, err := store.Load()
	if err != nil {
		return err
	}
	if len(registry) == 0 {
		_, _ = fmt.Fprintln(output, "No private links are configured. Run `katago-remote link create`.")
		return nil
	}
	_, _ = fmt.Fprintf(output, "%-10s %-14s %s\n", "ID", "MAX CLIENTS", "URL")
	for _, link := range registry {
		value, ok := linkURL(home, link)
		if !ok {
			value = "token: " + link.Token
		}
		_, _ = fmt.Fprintf(output, "%-10s %-14s %s\n", link.ID, links.DescribeMax(link.MaxClients), value)
	}
	return nil
}

func SetLinkMaxClients(home, ref string, maxClients int, output io.Writer) error {
	store, err := linkStore(home)
	if err != nil {
		return err
	}
	updated, err := store.SetMaxClients(ref, maxClients)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Link %s: max clients = %s\n", updated.ID, links.DescribeMax(updated.MaxClients))
	return nil
}

func DeleteLink(home, ref string, output io.Writer) error {
	store, err := linkStore(home)
	if err != nil {
		return err
	}
	deleted, err := store.Delete(ref)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Deleted link %s. Active sessions on it are disconnected within a few seconds.\n", deleted.ID)
	return nil
}

func Doctor(ctx context.Context, home string) []Check {
	checks := []Check{
		{Name: "Operating system", OK: runtime.GOOS == "linux", Message: runtime.GOOS + "/" + runtime.GOARCH},
		{Name: "Architecture", OK: runtime.GOARCH == "amd64", Message: runtime.GOARCH},
	}
	cfg, err := appconfig.Load(home)
	if err != nil {
		return append(checks, Check{Name: "Configuration", OK: false, Message: err.Error()})
	}
	checks = append(checks, Check{Name: "Configuration", OK: true, Message: filepath.Join(home, appconfig.ConfigFileName)})
	if store, storeErr := linkStore(home); storeErr == nil {
		if registry, loadErr := store.Load(); loadErr == nil {
			checks = append(checks, Check{
				Name:    "Private links",
				OK:      len(registry) > 0,
				Message: fmt.Sprintf("%d configured", len(registry)),
			})
		}
	}
	if cfg.NamedTunnel() {
		checks = append(checks,
			Check{Name: "Tunnel hostname", OK: true, Message: "https://" + cfg.TunnelHostname},
			Check{
				Name:    "Tunnel token",
				OK:      appconfig.HasTunnelToken(home),
				Message: filepath.Join(home, appconfig.TunnelTokenFileName),
			},
			Check{
				Name:    "Tunnel ingress",
				OK:      true,
				Message: "Cloudflare Public Hostname must target http://" + cfg.Listen,
			},
		)
	} else {
		checks = append(checks, Check{
			Name:    "Tunnel mode",
			OK:      true,
			Message: "quick (temporary trycloudflare.com hostname)",
		})
	}
	for name, path := range map[string]string{
		"KataGo":          cfg.KataGoPath,
		"Model":           cfg.ModelPath,
		"Analysis config": cfg.AnalysisConfig,
		"cloudflared":     cfg.CloudflaredPath,
	} {
		info, statErr := os.Stat(path)
		checks = append(checks, Check{Name: name, OK: statErr == nil && !infoIsDirectory(info), Message: path})
	}
	if cfg.HumanModelPath != "" {
		info, statErr := os.Stat(cfg.HumanModelPath)
		checks = append(checks, Check{Name: "Human model", OK: statErr == nil && !infoIsDirectory(info), Message: cfg.HumanModelPath})
	}
	checks = append(checks, commandCheck(ctx, "NVIDIA driver", "nvidia-smi", "--query-gpu=name,driver_version,memory.total", "--format=csv,noheader"))
	checks = append(checks, commandCheckWithEnv(ctx, "KataGo startup", cfg.ExtraEnvironment, cfg.KataGoPath, "version"))
	return checks
}

func infoIsDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir()
}

func commandCheck(ctx context.Context, name, command string, args ...string) Check {
	return commandCheckWithEnv(ctx, name, nil, command, args...)
}

func commandCheckWithEnv(ctx context.Context, name string, environment []string, command string, args ...string) Check {
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, command, args...)
	cmd.Env = append(os.Environ(), environment...)
	data, err := cmd.CombinedOutput()
	message := strings.TrimSpace(string(data))
	if len(message) > 500 {
		message = message[:500] + "..."
	}
	if err != nil {
		if message == "" {
			message = err.Error()
		}
		return Check{Name: name, OK: false, Message: message}
	}
	if message == "" {
		message = "OK"
	}
	return Check{Name: name, OK: true, Message: message}
}

func TailLog(home string, output io.Writer, lines int) error {
	if lines <= 0 {
		lines = 80
	}
	file, err := os.Open(filepath.Join(home, appconfig.LogFileName))
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]string, lines)
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		buffer[count%lines] = scanner.Text()
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	start := 0
	if count > lines {
		start = count % lines
	}
	limit := count
	if limit > lines {
		limit = lines
	}
	for i := 0; i < limit; i++ {
		_, _ = fmt.Fprintln(output, buffer[(start+i)%lines])
	}
	return nil
}
